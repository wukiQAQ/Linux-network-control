//go:build !linux

package capture

import "errors"

// errLiveUnsupported 是非 Linux 平台统一的错误信息。
const errLiveUnsupported = "live 抓包仅支持 Linux（可改用 synthetic/replay 数据源）"

// NewLive 在非 Linux 平台返回错误（live 抓包依赖 Linux AF_PACKET 套接字）。
func NewLive(_ string) (Source, error) {
	return nil, errors.New(errLiveUnsupported)
}

// NewLiveWithReaders 在非 Linux 平台同样返回错误。
// 多队列收包基于 Linux AF_PACKET 的 PACKET_FANOUT，其他系统没有等价实现；
// 这里保留同名函数是为了让上层的调用点在 Windows/macOS 上也能编译通过。
func NewLiveWithReaders(_ string, _ int) (Source, error) {
	return nil, errors.New(errLiveUnsupported)
}
