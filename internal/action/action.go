// Package action 提供"白名单运维动作"：由服务端声明动作与将要执行的命令，
// 客户端在执行前可以完整看到这些命令（命令预览），执行结果与审计记录一并返回。
//
// 设计原则：
//   - 只允许目录中登记的动作，不接受任意命令；
//   - 参数必须通过正则校验（服务端强制，客户端只做同样的预校验用于提示）；
//   - 危险动作（重启/停止服务等）必须显式 confirm；
//   - 所有执行都会写入内存审计历史（最近 N 条）。
package action

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Param 描述动作的一个输入参数。
type Param struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Required    bool   `json:"required"`
	Pattern     string `json:"pattern,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// Action 是一个可执行的白名单动作。
type Action struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Danger      bool     `json:"danger"`
	Params      []Param  `json:"params"`
	Commands    []string `json:"commands"`
}

// Result 是一次执行的结果（含实际执行的命令，便于前端原样展示）。
type Result struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Commands   []string          `json:"commands"`
	Params     map[string]string `json:"params"`
	ExitCode   int               `json:"exit_code"`
	Stdout     string            `json:"stdout"`
	Stderr     string            `json:"stderr"`
	DurationMS int64             `json:"duration_ms"`
	StartedAt  time.Time         `json:"started_at"`
	Truncated  bool              `json:"truncated"`
}

// Entry 是一条审计记录。
type Entry struct {
	ID         string            `json:"id"`
	ActionID   string            `json:"action_id"`
	Title      string            `json:"title"`
	Params     map[string]string `json:"params"`
	Commands   []string          `json:"commands"`
	ExitCode   int               `json:"exit_code"`
	DurationMS int64             `json:"duration_ms"`
	StartedAt  time.Time         `json:"started_at"`
}

var (
	// ErrUnknownAction 动作不在白名单中。
	ErrUnknownAction = errors.New("未知动作（不在白名单中）")
	// ErrConfirmRequired 危险动作需要显式确认。
	ErrConfirmRequired = errors.New("该动作会修改系统状态，需要确认后执行")
	// ErrInvalidParam 参数不合法。
	ErrInvalidParam = errors.New("参数不合法")
)

const (
	// DefaultTimeout 单个命令的执行超时。
	DefaultTimeout = 10 * time.Second
	// DefaultMaxOutput 单个命令回传的最大字节数（超出会截断并标记）。
	DefaultMaxOutput = 64 * 1024
	// maxHistory 审计历史上限。
	maxHistory = 200
)

// servicePattern 服务名：systemd 允许的字符集。
const servicePattern = "^[A-Za-z0-9_.@-]{1,64}$"

// pathPattern 绝对路径：只允许安全字符，不接受空格与 shell 元字符。
const pathPattern = "^/([A-Za-z0-9._@+-]+/)*[A-Za-z0-9._@+-]*$"

var serviceParam = Param{
	Name: "service", Label: "服务名", Placeholder: "例如 sshd / nginx", Required: true,
	Pattern: servicePattern, Hint: "只允许字母、数字与 _ . @ -",
}

var pathParam = Param{
	Name: "path", Label: "绝对路径", Placeholder: "例如 /var/log", Required: true,
	Pattern: pathPattern, Hint: "必须是绝对路径，不能包含空格或 ..",
}

// catalog 是全部可用动作（按分类分组，客户端据此渲染按钮与命令预览）。
var catalog = []Action{
	// ---------- 系统 ----------
	{ID: "system.os", Category: "system", Title: "系统信息", Description: "内核版本与发行版信息",
		Commands: []string{"uname -a", "cat /etc/os-release"}},
	{ID: "system.disk", Category: "system", Title: "磁盘使用率", Description: "各挂载点容量与剩余空间",
		Commands: []string{"df -h"}},
	{ID: "system.memory", Category: "system", Title: "内存使用", Description: "内存与交换分区（单位 MB）",
		Commands: []string{"free -m"}},
	{ID: "system.load", Category: "system", Title: "负载与运行时长", Description: "1/5/15 分钟负载与开机时长",
		Commands: []string{"uptime"}},
	{ID: "system.top", Category: "system", Title: "CPU 占用 TOP15", Description: "按 CPU 占用排序的进程列表",
		Commands: []string{"ps -eo pid,ppid,user,comm,pcpu,pmem,etime --sort=-pcpu | head -n 16"}},
	{ID: "system.users", Category: "system", Title: "登录用户", Description: "当前登录会话",
		Commands: []string{"who -a | head -n 20"}},

	// ---------- 网络 ----------
	{ID: "net.addrs", Category: "network", Title: "网卡与地址", Description: "所有网卡的 IP 与状态",
		Commands: []string{"ip -br a"}},
	{ID: "net.route", Category: "network", Title: "路由表", Description: "默认网关与路由条目",
		Commands: []string{"ip route"}},
	{ID: "net.listen", Category: "network", Title: "监听端口", Description: "正在监听的 TCP/UDP 端口",
		Commands: []string{"ss -tuln"}},
	{ID: "net.stat", Category: "network", Title: "连接统计", Description: "各类套接字数量汇总",
		Commands: []string{"ss -s"}},
	{ID: "net.ping", Category: "network", Title: "连通性测试", Description: "从 Linux 侧 ping 目标地址",
		Params: []Param{{Name: "target", Label: "目标地址", Placeholder: "例如 8.8.8.8 或 www.baidu.com",
			Required: true, Pattern: "^[A-Za-z0-9.:-]{1,64}$", Hint: "IP 或域名，不能包含空格"}},
		Commands: []string{"ping -c 4 {target}"}},

	// ---------- 服务 ----------
	{ID: "service.status", Category: "service", Title: "服务状态", Description: "查看 systemd 服务状态与开机自启",
		Params:   []Param{serviceParam},
		Commands: []string{"systemctl status {service} --no-pager -l", "systemctl is-enabled {service}"}},
	{ID: "service.start", Category: "service", Title: "启动服务", Description: "启动指定 systemd 服务",
		Params:   []Param{serviceParam},
		Commands: []string{"systemctl start {service}", "systemctl is-active {service}"}},
	{ID: "service.restart", Category: "service", Title: "重启服务", Description: "重启指定服务并确认结果",
		Danger: true, Params: []Param{serviceParam},
		Commands: []string{"systemctl restart {service}", "systemctl is-active {service}"}},
	{ID: "service.stop", Category: "service", Title: "停止服务", Description: "停止指定服务",
		Danger: true, Params: []Param{serviceParam},
		Commands: []string{"systemctl stop {service}", "systemctl is-active {service}"}},

	// ---------- 日志 ----------
	{ID: "log.journal", Category: "logs", Title: "系统日志尾部", Description: "journalctl 最近 100 行",
		Commands: []string{"journalctl -n 100 --no-pager"}},
	{ID: "log.service", Category: "logs", Title: "服务日志尾部", Description: "指定服务的最近 100 行日志",
		Params:   []Param{serviceParam},
		Commands: []string{"journalctl -u {service} -n 100 --no-pager"}},
	{ID: "log.dmesg", Category: "logs", Title: "内核日志尾部", Description: "dmesg 最近 50 行",
		Commands: []string{"dmesg | tail -n 50"}},

	// ---------- 文件 ----------
	{ID: "file.list", Category: "files", Title: "目录列表", Description: "列出目录内容与大小",
		Params:   []Param{pathParam},
		Commands: []string{"ls -lh {path}"}},
	{ID: "file.du", Category: "files", Title: "目录占用", Description: "统计目录总占用",
		Params:   []Param{pathParam},
		Commands: []string{"du -sh {path}"}},
	{ID: "file.tail", Category: "files", Title: "文件尾部", Description: "查看文件最近 50 行",
		Params:   []Param{pathParam},
		Commands: []string{"tail -n 50 {path}"}},
}

// Catalog 返回动作清单副本。
func Catalog() []Action {
	out := make([]Action, len(catalog))
	copy(out, catalog)
	return out
}

// Find 按 ID 查找动作。
func Find(id string) (Action, bool) {
	for _, a := range catalog {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// validateParams 校验参数并返回可用于命令替换的映射。
func validateParams(a Action, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(a.Params))
	for _, p := range a.Params {
		v := strings.TrimSpace(in[p.Name])
		if v == "" {
			if p.Required {
				return nil, fmt.Errorf("%w：%s 不能为空", ErrInvalidParam, p.Label)
			}
			out[p.Name] = ""
			continue
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return nil, fmt.Errorf("动作配置错误：%s", p.Name)
			}
			if !re.MatchString(v) {
				hint := p.Hint
				if hint == "" {
					hint = "格式不正确"
				}
				return nil, fmt.Errorf("%w：%s %s", ErrInvalidParam, p.Label, hint)
			}
		}
		if strings.Contains(v, "..") {
			return nil, fmt.Errorf("%w：%s 不允许包含 ..", ErrInvalidParam, p.Label)
		}
		out[p.Name] = v
	}
	return out, nil
}

// Resolve 把参数代入命令模板，得到"将会真正执行"的命令列表。
func Resolve(a Action, params map[string]string) ([]string, error) {
	clean, err := validateParams(a, params)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(a.Commands))
	for _, c := range a.Commands {
		cmd := c
		for k, v := range clean {
			cmd = strings.ReplaceAll(cmd, "{"+k+"}", v)
		}
		out = append(out, cmd)
	}
	return out, nil
}

// Runner 执行一条命令，返回 stdout / stderr / 退出码。
type Runner func(ctx context.Context, command string) (string, string, int)

// Executor 负责执行白名单动作并记录审计历史。
type Executor struct {
	mu        sync.Mutex
	history   []Entry
	seq       int64
	timeout   time.Duration
	maxOutput int
	runner    Runner
}

// NewExecutor 创建执行器（使用默认的 shell 执行方式）。
func NewExecutor() *Executor {
	return NewExecutorWithRunner(shellRunner)
}

// NewExecutorWithRunner 用自定义执行函数创建执行器（便于测试）。
func NewExecutorWithRunner(r Runner) *Executor {
	if r == nil {
		r = shellRunner
	}
	return &Executor{timeout: DefaultTimeout, maxOutput: DefaultMaxOutput, runner: r}
}

// History 返回审计历史（按时间倒序）。
func (e *Executor) History() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Entry, len(e.history))
	for i, v := range e.history {
		out[len(e.history)-1-i] = v
	}
	return out
}

// Run 执行一个白名单动作。
func (e *Executor) Run(ctx context.Context, id string, params map[string]string, confirm bool) (Result, error) {
	a, ok := Find(id)
	if !ok {
		return Result{}, ErrUnknownAction
	}
	commands, err := Resolve(a, params)
	if err != nil {
		return Result{}, err
	}
	if a.Danger && !confirm {
		return Result{}, ErrConfirmRequired
	}

	started := time.Now().UTC()
	res := Result{ID: a.ID, Title: a.Title, Commands: commands, Params: params, StartedAt: started}
	var stdout, stderr strings.Builder
	for _, c := range commands {
		cctx, cancel := context.WithTimeout(ctx, e.timeout)
		out, errOut, code := e.runner(cctx, c)
		cancel()
		if code != 0 && res.ExitCode == 0 {
			res.ExitCode = code
		}
		if strings.TrimSpace(out) != "" {
			stdout.WriteString("$ " + c + "\n" + out)
			if !strings.HasSuffix(out, "\n") {
				stdout.WriteString("\n")
			}
		} else {
			stdout.WriteString("$ " + c + "\n")
		}
		if strings.TrimSpace(errOut) != "" {
			stderr.WriteString(errOut)
			if !strings.HasSuffix(errOut, "\n") {
				stderr.WriteString("\n")
			}
		}
	}
	res.DurationMS = time.Since(started).Milliseconds()
	res.Stdout, res.Truncated = truncate(stdout.String(), e.maxOutput)
	if s, t := truncate(stderr.String(), e.maxOutput); true {
		res.Stderr = s
		res.Truncated = res.Truncated || t
	}

	e.mu.Lock()
	e.seq++
	entry := Entry{
		ID: fmt.Sprintf("run-%d", e.seq), ActionID: a.ID, Title: a.Title, Params: params,
		Commands: commands, ExitCode: res.ExitCode, DurationMS: res.DurationMS, StartedAt: started,
	}
	e.history = append(e.history, entry)
	if len(e.history) > maxHistory {
		e.history = e.history[len(e.history)-maxHistory:]
	}
	e.mu.Unlock()
	return res, nil
}

func truncate(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + "\n...（输出已截断）", true
}

// shellRunner 用系统 shell 执行命令：Linux 用 sh -lc，Windows 用 cmd /C（本地开发用）。
func shellRunner(ctx context.Context, command string) (string, string, int) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-lc", command)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			if errBuf.Len() == 0 {
				errBuf.WriteString(err.Error())
			}
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		if errBuf.Len() > 0 {
			errBuf.WriteString("\n")
		}
		errBuf.WriteString("命令执行超时")
		if code == 0 {
			code = -1
		}
	}
	return out.String(), errBuf.String(), code
}
