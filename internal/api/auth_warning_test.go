package api

import (
	"strings"
	"testing"
)

// TestAuthWarning 锁定 H2 的提醒触发条件：只有「没有 token」且「监听地址可能被同网段访问」时才提醒。
// 这里用表驱动把容易判断错的监听写法全部钉住（`:8080` 与 `0.0.0.0` 是暴露，回环写法不提醒）。
func TestAuthWarning(t *testing.T) {
	cases := []struct {
		name        string
		token       string
		listen      string
		wantWarning bool
	}{
		{"默认配置：无 token + 所有网卡", "", ":8080", true},
		{"显式所有网卡 IPv4", "", "0.0.0.0:8080", true},
		{"显式所有网卡 IPv6", "", "[::]:8080", true},
		{"空监听地址", "", "", true},
		{"局域网地址", "", "192.168.1.10:8080", true},
		{"回环 IPv4", "", "127.0.0.1:8080", false},
		{"回环 IPv6", "", "[::1]:8080", false},
		{"回环 IPv4 其他地址", "", "127.0.0.53:8080", false},
		{"localhost 主机名", "", "localhost:8080", false},
		{"localhost 无端口", "", "localhost", false},
		{"有 token 时一律不提醒（即使监听所有网卡）", "s3cret", ":8080", false},
		{"有 token + 回环也不提醒", "s3cret", "127.0.0.1:8080", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AuthWarning(tc.token, tc.listen)
			if tc.wantWarning {
				if got == "" {
					t.Fatalf("token=%q listen=%q 期望给出安全提醒，实际为空", tc.token, tc.listen)
				}
				if !strings.Contains(got, "token") || !strings.Contains(got, tc.listen) {
					t.Fatalf("提醒文案应同时提到 token 与监听地址，实际为 %q", got)
				}
				return
			}
			if got != "" {
				t.Fatalf("token=%q listen=%q 期望不提醒，实际为 %q", tc.token, tc.listen, got)
			}
		})
	}
}

// TestDefaultConfigTriggersAuthWarning 直接钉住「默认配置就会提醒」这一事实：
// internal/config 的 Default() 是 `:8080` + 空 token，两者组合必须命中提醒。
func TestDefaultConfigTriggersAuthWarning(t *testing.T) {
	if w := AuthWarning("", ":8080"); w == "" {
		t.Fatal("默认配置（无 token + :8080）必须给出安全提醒")
	}
}
