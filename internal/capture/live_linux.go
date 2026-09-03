//go:build linux

// live 数据源：使用 AF_PACKET 原始套接字直读网卡（需要 root / CAP_NET_RAW）。
// MVP 采用"每包一次 read"的简单模型，性能优化（PACKET_MMAP 环形缓冲）留待 V2。
package capture

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const ethPAll = 0x0003 // ETH_P_ALL：接收所有协议

// Live 是 Linux AF_PACKET 抓包实现。
type Live struct {
	fd     int
	iface  string
	once   sync.Once
	closed atomic.Bool
	stats  atomicStats
}

// NewLive 打开指定网卡的原始套接字。
func NewLive(iface string) (*Live, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(htons(ethPAll)))
	if err != nil {
		return nil, err
	}
	sa := &syscall.SockaddrLinklayer{
		Protocol: htons(ethPAll),
		Ifindex:  ifi.Index,
	}
	if err := syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	// 增大内核接收缓冲，降低高流量下丢包概率
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4<<20)
	return &Live{fd: fd, iface: iface}, nil
}

// Next 阻塞读取一个原始帧。
func (l *Live) Next(_ context.Context) (*Packet, error) {
	buf := make([]byte, 65536)
	for {
		n, _, err := syscall.Recvfrom(l.fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			l.stats.addDrop()
			return nil, err
		}
		if n == 0 {
			continue
		}
		l.stats.addPacket()
		raw := make([]byte, n)
		copy(raw, buf[:n])
		return &Packet{Ts: time.Now().UTC(), Raw: raw, Iface: l.iface}, nil
	}
}

func (l *Live) Close() error {
	var err error
	l.once.Do(func() {
		if l.closed.CompareAndSwap(false, true) {
			err = syscall.Close(l.fd)
		}
	})
	return err
}

func (l *Live) Stats() Stats { return l.stats.snapshot() }

// htons 将主机字节序转为网络字节序（x86 小端下即字节交换）。
func htons(v uint16) uint16 { return v<<8 | v>>8 }
