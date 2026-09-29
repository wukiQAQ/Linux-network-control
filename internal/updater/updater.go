// Package updater 实现"安全版自升级"：受控下载 → SHA256 校验 → 原子替换 → 可选重启。
//
// 安全约束（默认全部生效，任一条不满足都拒绝升级）：
//  1. 必须在配置里显式开启（[update] enabled = true）；
//  2. 下载地址必须匹配白名单（allowed_prefixes，为空则一律拒绝；按解析后的
//     scheme + host（含端口）+ path 前缀比对，重定向的每一跳都要重新校验）；
//  3. 必须提供并匹配 SHA256（64 位十六进制）；
//  4. 下载内容必须是 Linux ELF 可执行文件，且有大小上限；
//  5. 替换前备份原文件，替换用同目录 rename（原子）；失败不会破坏现有二进制。
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrDisabled 未在配置中开启自升级。
	ErrDisabled = errors.New("自升级未启用（需在配置中设置 [update] enabled = true）")
	// ErrURLNotAllowed 下载地址不在白名单内。
	ErrURLNotAllowed = errors.New("下载地址不在允许列表内")
	// ErrBadSHA 未提供或格式错误的 SHA256。
	ErrBadSHA = errors.New("必须提供 64 位十六进制的 SHA256")
	// ErrSHAMismatch 下载内容与声明的 SHA256 不一致。
	ErrSHAMismatch = errors.New("SHA256 校验失败，文件可能被篡改或上传错误")
	// ErrNotELF 下载内容不是 Linux 可执行文件。
	ErrNotELF = errors.New("下载内容不是 Linux ELF 可执行文件")
	// ErrTooLarge 超过大小上限。
	ErrTooLarge = errors.New("文件超过大小上限")
)

// Config 是自升级配置。
type Config struct {
	Enabled         bool
	AllowedPrefixes []string
	MaxBytes        int64
	Service         string
}

// Status 记录最近一次升级尝试（供 API 查询）。
type Status struct {
	Enabled    bool   `json:"enabled"`
	LastURL    string `json:"last_url,omitempty"`
	LastSHA256 string `json:"last_sha256,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastAt     string `json:"last_at,omitempty"`
	Applied    bool   `json:"applied"`
}

// Runner 执行外部命令（可注入，便于测试）。
type Runner func(name string, args ...string) error

// Updater 是自升级执行器。
type Updater struct {
	cfg    Config
	client *http.Client
	runner Runner
	mu     sync.Mutex
	last   Status
}

// New 创建执行器；默认 runner 会调用 systemctl 重启服务。
func New(cfg Config) *Updater {
	u := &Updater{
		cfg: cfg,
		runner: func(name string, args ...string) error {
			return execCommand(name, args...)
		},
		last: Status{Enabled: cfg.Enabled},
	}
	u.client = &http.Client{
		Timeout: 60 * time.Second,
		// 重定向的每一跳都要重新过白名单，否则一个被允许的 302 就能把请求带出白名单。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := checkAllowedURL(u.cfg, req.URL.String()); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("重定向次数过多")
			}
			return nil
		},
	}
	return u
}

// WithRunner 注入自定义命令执行器（测试用）。
func (u *Updater) WithRunner(r Runner) *Updater {
	if r != nil {
		u.runner = r
	}
	return u
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.last
	st.Enabled = u.cfg.Enabled
	return st
}

func (u *Updater) record(url, sha string, err error, applied bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = Status{
		Enabled: u.cfg.Enabled, LastURL: url, LastSHA256: sha,
		LastAt: time.Now().UTC().Format(time.RFC3339), Applied: applied,
	}
	if err != nil {
		u.last.LastError = err.Error()
	}
}

// allowedURL 校验协议与白名单。
func (u *Updater) allowedURL(raw string) error {
	return checkAllowedURL(u.cfg, raw)
}

// checkAllowedURL 判断 raw 是否命中白名单：
//   - 请求自身必须是 http/https；
//   - 与白名单条目逐项比对「解析后」的 scheme、Host（含端口语义）与 Path 前缀。
//
// 直接对未解析的原始字符串做前缀比较是不安全的：形如
// https://dl.example.com@evil.com/releases/x 的地址，其 userinfo 是
// dl.example.com、真实 Host 是 evil.com，原始前缀能匹配但请求会发往 evil.com。
func checkAllowedURL(cfg Config, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ErrURLNotAllowed
	}
	if len(cfg.AllowedPrefixes) == 0 {
		return fmt.Errorf("%w：未配置 allowed_prefixes", ErrURLNotAllowed)
	}
	for _, p := range cfg.AllowedPrefixes {
		if p == "" {
			continue
		}
		want, err := url.Parse(p)
		if err != nil || (want.Scheme != "http" && want.Scheme != "https") || want.Host == "" {
			continue
		}
		if !strings.EqualFold(parsed.Scheme, want.Scheme) {
			continue
		}
		if !strings.EqualFold(parsed.Host, want.Host) {
			continue
		}
		if strings.HasPrefix(parsed.Path, want.Path) {
			return nil
		}
	}
	return ErrURLNotAllowed
}

// Upgrade 下载 url 处的二进制，校验 SHA256 与 ELF 后原子替换 target，返回实际 SHA256。
func (u *Updater) Upgrade(ctx context.Context, rawURL, wantSHA, target string) (string, error) {
	if !u.cfg.Enabled {
		return "", ErrDisabled
	}
	want := strings.ToLower(strings.TrimSpace(wantSHA))
	if len(want) != 64 || !isHex(want) {
		return "", ErrBadSHA
	}
	if err := u.allowedURL(rawURL); err != nil {
		u.record(rawURL, want, err, false)
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		u.record(rawURL, want, err, false)
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
		u.record(rawURL, want, err, false)
		return "", err
	}

	max := u.cfg.MaxBytes
	if max <= 0 {
		max = 64 << 20
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".upgrade-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	hash := sha256.New()
	// 多读 1 字节用于判断是否超限
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, max+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if written > max {
		tmp.Close()
		os.Remove(tmpName)
		u.record(rawURL, want, ErrTooLarge, false)
		return "", ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		os.Remove(tmpName)
		u.record(rawURL, want, ErrSHAMismatch, false)
		return "", ErrSHAMismatch
	}
	if !isELF(tmpName) {
		os.Remove(tmpName)
		u.record(rawURL, want, ErrNotELF, false)
		return "", ErrNotELF
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	// 备份现有文件（同名 .bak.<时间戳>）
	if _, statErr := os.Stat(target); statErr == nil {
		backup := fmt.Sprintf("%s.bak.%s", target, time.Now().UTC().Format("20060102-150405"))
		if err := copyFile(target, backup); err != nil {
			os.Remove(tmpName)
			u.record(rawURL, want, err, false)
			return "", fmt.Errorf("备份失败: %w", err)
		}
	}
	// 同目录 rename：原子替换
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		u.record(rawURL, want, err, false)
		return "", fmt.Errorf("替换失败: %w", err)
	}
	u.record(rawURL, want, nil, true)
	return got, nil
}

// RestartAfter 延迟重启服务：先让 HTTP 响应返回，再重启进程。
func (u *Updater) RestartAfter(d time.Duration) {
	if u.cfg.Service == "" {
		return
	}
	go func() {
		time.Sleep(d)
		_ = u.runner("systemctl", "restart", u.cfg.Service)
	}()
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	return hdr[0] == 0x7f && hdr[1] == 'E' && hdr[2] == 'L' && hdr[3] == 'F'
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
