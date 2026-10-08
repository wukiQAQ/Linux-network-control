package api

import (
	"net"
	"strings"
)

// ListenURL 把 api.listen 的监听地址转换成可直接打开的 HTTP URL。
//
// 历史实现在启动日志里直接拼 "http://localhost" + listen：监听 ":8080" 时结果正确，
// 但监听 "127.0.0.1:18080" 时会打印出 "http://localhost127.0.0.1:18080"——一个点不开的
// 地址，用户只能自己猜真实端口（Windows 本机模式默认就用带主机的地址，必然踩到）。
//
// 规则：
//   - 只给端口（":8080"）：补上本机回环地址，得到 http://127.0.0.1:8080；
//   - 给了主机（"127.0.0.1:18080"、"0.0.0.0:8080"、"[::1]:8080"）：原样保留；
//   - 空字符串：返回空串，调用方据此跳过启动日志。
func ListenURL(listen string) string {
	addr := strings.TrimSpace(listen)
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// 不是 host:port 形态；只有 ":port" 这种缺主机的写法需要补回环地址。
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
	} else if host == "" {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	return "http://" + addr
}
