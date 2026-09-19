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
