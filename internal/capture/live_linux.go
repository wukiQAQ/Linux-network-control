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

	"golang.org/x/sys/unix"
)

const (
	ethPAll = 0x0003 // ETH_P_ALL：接收所有协议

	// PACKET_QDISC_BYPASS：跳过内核 qdisc 排队，减少一层延迟（Linux 3.14+）
	packetQdiscBypass = 20
	// PACKET_STATISTICS：读取内核侧的收包/丢包统计
	packetStatistics = 6
)

// Live 是 Linux AF_PACKET 抓包实现。
type Live struct {
	fd     int
	iface  string
	once   sync.Once
	closed atomic.Bool
	stats  atomicStats

	// 读缓冲复用：避免每包分配 64KB
	bufs sync.Pool
	// seen 为本进程已读包数，用于节流查询内核统计
	seen atomic.Uint64
	// kernelDrops 为内核上报的丢包数（比"仅在出错时计数"更真实）
	kernelDrops atomic.Uint64
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
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8<<20)
	// 跳过 qdisc 排队：减少内核侧延迟与排队丢包
	_ = unix.SetsockoptInt(fd, unix.SOL_PACKET, packetQdiscBypass, 1)

	l := &Live{fd: fd, iface: iface}
	l.bufs.New = func() any {
		b := make([]byte, 65536)
		return &b
	}
	return l, nil
}

// refreshKernelStats 读取内核侧统计（PACKET_STATISTICS），更新真实丢包数。
func (l *Live) refreshKernelStats() {
	st, err := unix.GetsockoptTpacketStats(l.fd, unix.SOL_PACKET, packetStatistics)
	if err != nil || st == nil {
		return
	}
	if st.Drops > 0 {
		l.kernelDrops.Store(uint64(st.Drops))
	}
}

// Next 阻塞读取一个原始帧（复用读缓冲，减少每包分配）。
func (l *Live) Next(_ context.Context) (*Packet, error) {
	bp := l.bufs.Get().(*[]byte)
	buf := *bp
	defer l.bufs.Put(bp)
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
		// 每 128 个包刷新一次内核统计（含真实丢包数）
		if shouldPollStats(l.seen.Add(1)) {
			l.refreshKernelStats()
		}
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

// Stats 返回收包数与丢包数（丢包 = 读取出错次数 + 内核上报丢包）。
func (l *Live) Stats() Stats {
	s := l.stats.snapshot()
	return Stats{Packets: s.Packets, Drops: s.Drops + l.kernelDrops.Load()}
}

// htons 将主机字节序转为网络字节序（x86 小端下即字节交换）。
func htons(v uint16) uint16 { return v<<8 | v>>8 }
