package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeELF 返回一段最小可识别的 ELF 内容（仅用于校验魔数，不要求可执行）。
func fakeELF(payload string) []byte {
	return append([]byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, []byte(payload)...)
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newTestUpdater(t *testing.T, maxBytes int64, prefixes ...string) (*Updater, string) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "netmon-linux")
	if err := os.WriteFile(target, fakeELF("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := New(Config{Enabled: true, AllowedPrefixes: prefixes, MaxBytes: maxBytes, Service: "netmon"})
	return u, target
}

func TestUpgradeDisabledAndValidation(t *testing.T) {
	u := New(Config{Enabled: false})
	if _, err := u.Upgrade(context.Background(), "http://x/y", strings.Repeat("a", 64), "t"); !errors.Is(err, ErrDisabled) {
		t.Errorf("未启用应返回 ErrDisabled，实际 %v", err)
	}

	u, target := newTestUpdater(t, 0, "http://127.0.0.1:1/")
	if _, err := u.Upgrade(context.Background(), "http://evil.example/x", strings.Repeat("a", 64), target); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("非白名单地址应被拒绝，实际 %v", err)
	}
	if _, err := u.Upgrade(context.Background(), "http://127.0.0.1:1/x", "not-a-sha", target); !errors.Is(err, ErrBadSHA) {
		t.Errorf("非法 SHA 应被拒绝，实际 %v", err)
	}
	// 协议不支持
	if _, err := u.Upgrade(context.Background(), "ftp://127.0.0.1/x", strings.Repeat("a", 64), target); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("非 http(s) 应被拒绝，实际 %v", err)
	}
}

func TestUpgradeHappyPath(t *testing.T) {
	body := fakeELF("new-binary-content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	u, target := newTestUpdater(t, 0, srv.URL+"/")
	got, err := u.Upgrade(context.Background(), srv.URL+"/netmon-linux", shaOf(body), target)
	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	if got != shaOf(body) {
		t.Errorf("返回的 SHA256 不正确: %s", got)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(body) {
		t.Fatalf("目标文件未被替换: %v", err)
	}
	// 备份存在
	matches, _ := filepath.Glob(target + ".bak.*")
	if len(matches) != 1 {
		t.Errorf("应生成 1 个备份，实际 %d", len(matches))
	}
	st := u.Status()
	if !st.Applied || st.LastError != "" || st.LastSHA256 != shaOf(body) {
		t.Errorf("状态记录不正确: %+v", st)
	}
}

func TestUpgradeRejectsBadContent(t *testing.T) {
	// 1) SHA 不匹配：目标文件必须保持原样
	body := fakeELF("tampered")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	u, target := newTestUpdater(t, 0, srv.URL+"/")
	if _, err := u.Upgrade(context.Background(), srv.URL+"/x", strings.Repeat("b", 64), target); !errors.Is(err, ErrSHAMismatch) {
		t.Errorf("SHA 不匹配应返回 ErrSHAMismatch，实际 %v", err)
	}
	cur, _ := os.ReadFile(target)
	if string(cur) != string(fakeELF("old")) {
		t.Error("校验失败时不应改动目标文件")
	}

	// 2) 不是 ELF
	notELF := []byte("#!/bin/sh\necho hi\n")
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(notELF) }))
	defer srv2.Close()
	u2, target2 := newTestUpdater(t, 0, srv2.URL+"/")
	if _, err := u2.Upgrade(context.Background(), srv2.URL+"/x", shaOf(notELF), target2); !errors.Is(err, ErrNotELF) {
		t.Errorf("非 ELF 应返回 ErrNotELF，实际 %v", err)
	}

	// 3) 超过大小上限
	big := fakeELF(strings.Repeat("x", 4096))
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(big) }))
	defer srv3.Close()
	u3, target3 := newTestUpdater(t, 128, srv3.URL+"/")
	if _, err := u3.Upgrade(context.Background(), srv3.URL+"/x", shaOf(big), target3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限应返回 ErrTooLarge，实际 %v", err)
	}
}

func TestUpgradeNoWhitelistConfigured(t *testing.T) {
	u, target := newTestUpdater(t, 0) // 不配置白名单
	body := fakeELF("x")
	if _, err := u.Upgrade(context.Background(), "http://127.0.0.1/x", shaOf(body), target); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("未配置白名单应一律拒绝，实际 %v", err)
	}
}

func TestRestartUsesInjectedRunner(t *testing.T) {
	u, _ := newTestUpdater(t, 0, "http://127.0.0.1/")
	called := make(chan string, 1)
	u.WithRunner(func(name string, args ...string) error {
		called <- name + " " + strings.Join(args, " ")
		return nil
	})
	u.RestartAfter(0)
	select {
	case got := <-called:
		if got != "systemctl restart netmon" {
			t.Errorf("重启命令不正确: %s", got)
		}
	case <-timeAfter():
		t.Error("未触发重启命令")
	}
}

// timeAfter 返回一个短超时通道（测试用）。
func timeAfter() <-chan time.Time { return time.After(2 * time.Second) }

// TestAllowedURLTable 覆盖白名单校验按「解析后的 scheme/host/path」比对的各种边界。
func TestAllowedURLTable(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []string
		raw      string
		ok       bool
	}{
		{"合法 https 前缀", []string{"https://dl.example.com/releases/"}, "https://dl.example.com/releases/netmon", true},
		{"合法 http 带端口", []string{"http://127.0.0.1:8080/dl/"}, "http://127.0.0.1:8080/dl/x", true},
		{"host 大小写不敏感", []string{"https://DL.Example.com/releases/"}, "https://dl.example.com/releases/x", true},
		{"端口不同", []string{"http://127.0.0.1:8080/dl/"}, "http://127.0.0.1:9090/dl/x", false},
		{"host 后缀混淆", []string{"https://dl.example.com/releases/"}, "https://dl.example.com.evil.com/releases/x", false},
		{"userinfo 绕过", []string{"https://dl.example.com/releases/"}, "https://dl.example.com@evil.com/releases/x", false},
		// 裸 host 白名单下，旧实现（对原始字符串做前缀比较）会放行下面这条：原始串确实以
		// https://dl.example.com 开头，但真实 Host 是 evil.com。
		{"userinfo 绕过（裸 host 白名单）", []string{"https://dl.example.com"}, "https://dl.example.com@evil.com/releases/x", false},
		{"路径越界", []string{"https://dl.example.com/releases/"}, "https://dl.example.com/other/x", false},
		{"路径前缀边界", []string{"https://dl.example.com/releases/"}, "https://dl.example.com/releases-evil/x", false},
		{"scheme 不符", []string{"https://dl.example.com/releases/"}, "http://dl.example.com/releases/x", false},
		{"file 协议", []string{"https://dl.example.com/releases/"}, "file:///etc/passwd", false},
		{"无 host", []string{"https://dl.example.com/releases/"}, "https:///releases/x", false},
		{"未配置白名单", nil, "https://dl.example.com/releases/x", false},
		{"白名单项非法", []string{"not-a-url", ""}, "https://dl.example.com/releases/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := New(Config{Enabled: true, AllowedPrefixes: tc.prefixes})
			err := u.allowedURL(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("应允许 %q，实际 %v", tc.raw, err)
			}
			if !tc.ok && !errors.Is(err, ErrURLNotAllowed) {
				t.Fatalf("应拒绝 %q 并返回 ErrURLNotAllowed，实际 %v", tc.raw, err)
			}
		})
	}
}

// TestUpgradeRejectsUserinfoBypass 确认 userinfo 绕过在 Upgrade 入口即被拦住。
// 白名单用裸 host，旧实现的原始串前缀比较会放行 https://dl.example.com@evil.com/...。
func TestUpgradeRejectsUserinfoBypass(t *testing.T) {
	u, target := newTestUpdater(t, 0, "https://dl.example.com")
	evil := "https://dl.example.com@evil.com/releases/netmon"
	if err := u.allowedURL(evil); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("allowedURL 应拒绝 userinfo 绕过，实际 %v", err)
	}
	if _, err := u.Upgrade(context.Background(), evil, strings.Repeat("a", 64), target); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("userinfo 绕过应返回 ErrURLNotAllowed，实际 %v", err)
	}
	cur, _ := os.ReadFile(target)
	if string(cur) != string(fakeELF("old")) {
		t.Error("被拒绝时不应改动目标文件")
	}
}

// TestUpgradeRejectsFileScheme 确认非 http/https 协议被拒绝。
func TestUpgradeRejectsFileScheme(t *testing.T) {
	u, target := newTestUpdater(t, 0, "https://dl.example.com/releases/")
	if _, err := u.Upgrade(context.Background(), "file:///etc/passwd", strings.Repeat("a", 64), target); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("file 协议应返回 ErrURLNotAllowed，实际 %v", err)
	}
}

// TestUpgradeRejectsOffWhitelistRedirect：白名单 server 用 302 指向非白名单 server，
// 必须拒绝，且绝不能把 evil 的内容落到目标文件上。
func TestUpgradeRejectsOffWhitelistRedirect(t *testing.T) {
	evilBody := fakeELF("evil-binary")
	var evilHits int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&evilHits, 1)
		w.Write(evilBody)
	}))
	defer evil.Close()

	white := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/releases/netmon", http.StatusFound)
	}))
	defer white.Close()

	u, target := newTestUpdater(t, 0, white.URL+"/releases/")
	_, err := u.Upgrade(context.Background(), white.URL+"/releases/netmon", shaOf(evilBody), target)
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("跳出白名单的重定向应返回 ErrURLNotAllowed，实际 %v", err)
	}
	if n := atomic.LoadInt32(&evilHits); n != 0 {
		t.Errorf("不应真正请求非白名单 server，实际命中 %d 次", n)
	}
	cur, _ := os.ReadFile(target)
	if string(cur) != string(fakeELF("old")) {
		t.Error("被拒绝时不应改动目标文件")
	}
}

// TestUpgradeFollowsWhitelistedRedirect：白名单内的重定向仍然允许（确认没有过度收紧）。
func TestUpgradeFollowsWhitelistedRedirect(t *testing.T) {
	body := fakeELF("redirected-new-binary")
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/real", http.StatusFound)
	})
	mux.HandleFunc("/releases/real", func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u, target := newTestUpdater(t, 0, srv.URL+"/releases/")
	if _, err := u.Upgrade(context.Background(), srv.URL+"/releases/start", shaOf(body), target); err != nil {
		t.Fatalf("白名单内的重定向应被允许，实际 %v", err)
	}
	cur, _ := os.ReadFile(target)
	if string(cur) != string(body) {
		t.Error("应替换为重定向后的最终内容")
	}
}
