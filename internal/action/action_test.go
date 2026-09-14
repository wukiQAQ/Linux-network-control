package action

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// TestCatalogIntegrity 校验动作目录本身是自洽的。
func TestCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	validCats := map[string]bool{"system": true, "network": true, "service": true, "logs": true, "files": true}
	for _, a := range Catalog() {
		if a.ID == "" || a.Title == "" || a.Category == "" || a.Description == "" {
			t.Errorf("动作字段不完整: %+v", a)
		}
		if seen[a.ID] {
			t.Errorf("动作 ID 重复: %s", a.ID)
		}
		seen[a.ID] = true
		if !validCats[a.Category] {
			t.Errorf("动作 %s 的分类 %q 未登记", a.ID, a.Category)
		}
		if len(a.Commands) == 0 {
			t.Errorf("动作 %s 没有命令", a.ID)
		}
		for _, p := range a.Params {
			if p.Name == "" || p.Label == "" {
				t.Errorf("动作 %s 的参数定义不完整: %+v", a.ID, p)
			}
			if p.Pattern != "" {
				if _, err := regexp.Compile(p.Pattern); err != nil {
					t.Errorf("动作 %s 参数 %s 的正则不合法: %v", a.ID, p.Name, err)
				}
			}
		}
	}
	if len(seen) < 15 {
		t.Errorf("动作数量偏少: %d", len(seen))
	}
	// 危险动作应当存在，且都需要参数（重启/停止服务）
	if _, ok := Find("service.restart"); !ok {
		t.Error("缺少 service.restart 动作")
	}
}

func TestResolve(t *testing.T) {
	disk, _ := Find("system.disk")
	cmds, err := Resolve(disk, nil)
	if err != nil || len(cmds) != 1 || cmds[0] != "df -h" {
		t.Fatalf("Resolve(disk) = %v, %v", cmds, err)
	}

	svc, _ := Find("service.status")
	cmds, err = Resolve(svc, map[string]string{"service": "sshd"})
	if err != nil {
		t.Fatalf("Resolve(service.status): %v", err)
	}
	if len(cmds) != 2 || !strings.Contains(cmds[0], "sshd") {
		t.Fatalf("命令未代入参数: %v", cmds)
	}
}

func TestValidateParams(t *testing.T) {
	svc, _ := Find("service.status")
	if _, err := Resolve(svc, map[string]string{}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("缺少必填参数应报 ErrInvalidParam，实际 %v", err)
	}
	if _, err := Resolve(svc, map[string]string{"service": "ssh; rm -rf /"}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("含 shell 元字符的服务名应被拒绝，实际 %v", err)
	}
	if _, err := Resolve(svc, map[string]string{"service": "a b"}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("含空格的服务名应被拒绝，实际 %v", err)
	}

	file, _ := Find("file.list")
	if _, err := Resolve(file, map[string]string{"path": "/var/../etc"}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("含 .. 的路径应被拒绝，实际 %v", err)
	}
	if _, err := Resolve(file, map[string]string{"path": "/var/log | cat /etc/passwd"}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("含管道符的路径应被拒绝，实际 %v", err)
	}
	if _, err := Resolve(file, map[string]string{"path": "relative/path"}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("相对路径应被拒绝，实际 %v", err)
	}
	if _, err := Resolve(file, map[string]string{"path": "/var/log"}); err != nil {
		t.Errorf("合法路径不应报错: %v", err)
	}
}

func fakeRunner(stdout, stderr string, code int) Runner {
	return func(ctx context.Context, command string) (string, string, int) {
		return stdout, stderr, code
	}
}

func TestRunSuccessAndAudit(t *testing.T) {
	e := NewExecutorWithRunner(fakeRunner("ok-out", "", 0))
	res, err := e.Run(context.Background(), "system.disk", nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "df -h") || !strings.Contains(res.Stdout, "ok-out") {
		t.Errorf("stdout 应包含命令与输出: %q", res.Stdout)
	}
	if len(res.Commands) != 1 || res.Commands[0] != "df -h" {
		t.Errorf("结果里应回带实际命令: %v", res.Commands)
	}
	h := e.History()
	if len(h) != 1 || h[0].ActionID != "system.disk" {
		t.Fatalf("审计历史不正确: %+v", h)
	}
}

func TestRunDangerNeedsConfirm(t *testing.T) {
	e := NewExecutorWithRunner(fakeRunner("active", "", 0))
	if _, err := e.Run(context.Background(), "service.restart", map[string]string{"service": "sshd"}, false); !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("危险动作未确认应报 ErrConfirmRequired，实际 %v", err)
	}
	if len(e.History()) != 0 {
		t.Error("未执行的动作不应写入审计历史")
	}
	res, err := e.Run(context.Background(), "service.restart", map[string]string{"service": "sshd"}, true)
	if err != nil {
		t.Fatalf("确认后应可执行: %v", err)
	}
	if !strings.Contains(strings.Join(res.Commands, " "), "systemctl restart sshd") {
		t.Errorf("命令不正确: %v", res.Commands)
	}
	if len(e.History()) != 1 {
		t.Errorf("审计历史应有 1 条，实际 %d", len(e.History()))
	}
}

func TestRunUnknownAction(t *testing.T) {
	e := NewExecutorWithRunner(fakeRunner("", "", 0))
	if _, err := e.Run(context.Background(), "system.rm-rf", nil, true); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("未知动作应报 ErrUnknownAction，实际 %v", err)
	}
}

func TestRunNonZeroExitAndStderr(t *testing.T) {
	e := NewExecutorWithRunner(fakeRunner("", "命令不存在", 127))
	res, err := e.Run(context.Background(), "system.disk", nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 127 {
		t.Errorf("ExitCode = %d，期望 127", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "命令不存在") {
		t.Errorf("stderr 未回传: %q", res.Stderr)
	}
}

func TestOutputTruncation(t *testing.T) {
	big := strings.Repeat("x", DefaultMaxOutput+100)
	e := NewExecutorWithRunner(fakeRunner(big, "", 0))
	res, err := e.Run(context.Background(), "system.disk", nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Error("超长输出应标记 Truncated")
	}
	if !strings.Contains(res.Stdout, "输出已截断") {
		t.Error("截断提示缺失")
	}
}

func TestHistoryOrder(t *testing.T) {
	e := NewExecutorWithRunner(fakeRunner("x", "", 0))
	for i := 0; i < 3; i++ {
		if _, err := e.Run(context.Background(), "system.load", nil, false); err != nil {
			t.Fatal(err)
		}
	}
	h := e.History()
	if len(h) != 3 {
		t.Fatalf("历史条数 = %d", len(h))
	}
	if !h[0].StartedAt.After(h[2].StartedAt) && !h[0].StartedAt.Equal(h[2].StartedAt) {
		t.Error("审计历史应为倒序（最新在前）")
	}
}
