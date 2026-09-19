//go:build linux

// live 数据源：使用 AF_PACKET 原始套接字直读网卡（需要 root / CAP_NET_RAW）。
// V0.15.0 起复用读缓冲、跳过 qdisc 排队并读取内核丢包统计；
// V0.17.0 起支持多队列（readers>1 时多套接字 + PACKET_FANOUT 并行收包）。
// 零拷贝（PACKET_MMAP 环形缓冲）仍在规划中，尚未实现。
package capture

import (
	"context"
	"errors"
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
	// PACKET_FANOUT：多个套接字按哈希分流，实现多队列并行收包（Linux 3.12+）
	packetFanout    = 18
	fanoutModeHash  = 0
	fanoutGroupMask = 0xffff

	// recvTimeoutUsec 是套接字的接收超时（微秒）。
	// 有了它，阻塞在 recvfrom 上的 goroutine 最多 200ms 就返回一次，
	// 于是能及时看到关闭标志并退出，Close() 不会永久等待。
	recvTimeoutUsec = 200 * 1000
)

// errSourceClosed 表示数据源已被关闭，不会再产出报文。
var errSourceClosed = errors.New("数据源已关闭")

// Live 是 Linux AF_PACKET 抓包实现。
// readers > 1 时使用多个套接字 + PACKET_FANOUT 并行收包（多队列）；
// 任何一步失败都会自动回退到单套接字模式，不影响抓包可用性。
type Live struct {
	fd      int   // 主套接字（用于内核统计与作为单队列读入口）
	fds     []int // 全部套接字（多队列时 > 1）
	iface   string
	once    sync.Once
	closed  atomic.Bool
	done    chan struct{} // 关闭信号：唤醒阻塞在通道发送上的读循环
	stats   atomicStats
	readers int
	pkts    chan *Packet
	wg      sync.WaitGroup

	// 读缓冲复用：避免每包分配 64KB
	bufs sync.Pool
	// seen 为本进程已读包数，用于节流查询内核统计
	seen atomic.Uint64
	// kernelDrops 为内核上报的丢包数（比"仅在出错时计数"更真实）
	kernelDrops atomic.Uint64
}

// NewLive 打开指定网卡的原始套接字（单队列）。
func NewLive(iface string) (*Live, error) {
	return NewLiveWithReaders(iface, MinReaders)
}

// NewLiveWithReaders 打开 1~8 个套接字并行收包（多队列）。
func NewLiveWithReaders(iface string, readers int) (*Live, error) {
	readers = normalizeReaders(readers)
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	open := func() (int, error) {
		fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(htons(ethPAll)))
		if err != nil {
			return -1, err
		}
		sa := &syscall.SockaddrLinklayer{Protocol: htons(ethPAll), Ifindex: ifi.Index}
		if err := syscall.Bind(fd, sa); err != nil {
			syscall.Close(fd)
			return -1, err
		}
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8<<20)
		_ = unix.SetsockoptInt(fd, unix.SOL_PACKET, packetQdiscBypass, 1)
		// 接收超时：让读循环能周期性醒来检查关闭标志
		_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: recvTimeoutUsec})
		return fd, nil
	}

	first, err := open()
	if err != nil {
		return nil, err
	}
	l := &Live{fd: first, fds: []int{first}, iface: iface, readers: 1, done: make(chan struct{})}
	l.bufs.New = func() any {
		b := make([]byte, 65536)
		return &b
	}
	if readers == 1 {
		return l, nil
	}

	// 多队列：再开 N-1 个套接字，并让内核按流哈希把包分流到同一组，
	// 这样同一条流的报文始终落到同一个套接字，不会打乱顺序。
	group := uint16(1)
	if len(iface) > 0 {
		group = uint16(iface[0])<<8 | uint16(iface[len(iface)-1])
	}
	group &= fanoutGroupMask
	fanoutVal := int(group) | (fanoutModeHash << 16)
	ok := true
	for i := 1; i < readers; i++ {
		fd, err := open()
		if err != nil {
			ok = false
			break
		}
		if err := unix.SetsockoptInt(fd, unix.SOL_PACKET, packetFanout, fanoutVal); err != nil {
			syscall.Close(fd)
			ok = false
			break
		}
		l.fds = append(l.fds, fd)
	}
	if ok {
		// 主套接字也要加入同一 fanout 组，否则它会把所有包再收一份
		if err := unix.SetsockoptInt(first, unix.SOL_PACKET, packetFanout, fanoutVal); err != nil {
			ok = false
		}
	}
	if !ok {
		// 回退：关闭多余套接字，保持单队列
		for _, fd := range l.fds[1:] {
			syscall.Close(fd)
		}
		l.fds = l.fds[:1]
		return l, nil
	}
	l.readers = len(l.fds)
	l.pkts = make(chan *Packet, 1024*l.readers)
	l.wg.Add(l.readers)
	for i := 0; i < l.readers; i++ {
		go l.readLoop(l.fds[i])
	}
	return l, nil
}

// readLoop 是单个套接字的读循环（多队列模式）。
func (l *Live) readLoop(fd int) {
	defer l.wg.Done()
	bp := l.bufs.Get().(*[]byte)
	buf := *bp
	defer l.bufs.Put(bp)
	for {
		if l.closed.Load() {
			return
		}
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR || isRecvTimeout(err) {
				continue // 被信号打断或接收超时：回到循环顶部检查关闭标志
			}
			l.stats.addDrop()
			return // 套接字已关闭或其他错误
		}
		if n == 0 {
			continue
		}
		l.stats.addPacket()
		// 每 128 个包刷新一次内核统计（含真实丢包数），多队列时由各循环共享计数
		if shouldPollStats(l.seen.Add(1)) {
			l.refreshKernelStats()
		}
		raw := make([]byte, n)
		copy(raw, buf[:n])
		p := &Packet{Ts: time.Now().UTC(), Raw: raw, Iface: l.iface}
		select {
		case l.pkts <- p:
		case <-l.done:
			return
		}
	}
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

// Next 阻塞读取一个原始帧；多队列模式下从读循环汇入的通道取包。
func (l *Live) Next(ctx context.Context) (*Packet, error) {
	if l.readers > 1 {
		select {
		case p, ok := <-l.pkts:
			if !ok {
				return nil, errSourceClosed
			}
			return p, nil
		case <-l.done:
			// 关闭时把通道里已经读到的包尽量发完，避免丢掉尾部数据
			select {
			case p, ok := <-l.pkts:
				if !ok {
					return nil, errSourceClosed
				}
				return p, nil
			default:
				return nil, errSourceClosed
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return l.nextSingle(ctx)
}

// nextSingle 单队列模式：直接在当前 goroutine 读取（复用读缓冲）。
func (l *Live) nextSingle(ctx context.Context) (*Packet, error) {
	bp := l.bufs.Get().(*[]byte)
	buf := *bp
	defer l.bufs.Put(bp)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if l.closed.Load() {
			return nil, errSourceClosed
		}
		n, _, err := syscall.Recvfrom(l.fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR || isRecvTimeout(err) {
				continue // 超时后回到顶部检查 ctx 与关闭标志
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

// Close 关闭全部套接字并等待读循环退出，可重复调用。
func (l *Live) Close() error {
	var err error
	l.once.Do(func() {
		if !l.closed.CompareAndSwap(false, true) {
			return
		}
		close(l.done)
		// 先等读循环退出（受 SO_RCVTIMEO 约束，最多约 200ms），再关套接字，
		// 避免"关闭 fd 后文件描述符号被复用"导致读到别的连接的数据。
		l.wg.Wait()
		for _, fd := range l.fds {
			if cerr := syscall.Close(fd); cerr != nil && err == nil {
				err = cerr
			}
		}
		if l.readers > 1 {
			close(l.pkts)
		}
	})
	return err
}

// Stats 返回收包数与丢包数（丢包 = 读取出错次数 + 内核上报丢包）。
func (l *Live) Stats() Stats {
	s := l.stats.snapshot()
	return Stats{Packets: s.Packets, Drops: s.Drops + l.kernelDrops.Load()}
}

// isRecvTimeout 判断错误是否为接收超时（SO_RCVTIMEO 到期）。
func isRecvTimeout(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.ETIMEDOUT)
}

// htons 将主机字节序转为网络字节序（x86 小端下即字节交换）。
func htons(v uint16) uint16 { return v<<8 | v>>8 }
