//go:build !linux

package capture

import "errors"

// NewLive 在非 Linux 平台返回错误（live 抓包依赖 Linux AF_PACKET 套接字）。
func NewLive(_ string) (Source, error) {
	return nil, errors.New("live 抓包仅支持 Linux（可改用 synthetic/replay 数据源）")
}
