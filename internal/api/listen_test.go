package api

import "testing"

// TestListenURL 校验监听地址到可点击 URL 的转换。
// 重点是带主机的地址不能被拼成 "http://localhost127.0.0.1:18080"（历史缺陷）。
func TestListenURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{":8080", "http://127.0.0.1:8080"},
		{":18080", "http://127.0.0.1:18080"},
		{"  :9090  ", "http://127.0.0.1:9090"},
		{"127.0.0.1:18080", "http://127.0.0.1:18080"},
		{"0.0.0.0:8080", "http://0.0.0.0:8080"},
		{"localhost:8080", "http://localhost:8080"},
		{"[::1]:8080", "http://[::1]:8080"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ListenURL(c.in); got != c.want {
			t.Errorf("ListenURL(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
