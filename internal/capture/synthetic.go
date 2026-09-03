// synthetic 数据源：按设定速率（pps）在进程内构造以太网/IPv4/TCP|UDP 报文，
// 用于开发、演示与集成测试，不需要 root 权限和真实网卡。
package capture

import (
	"context"
	"math/rand"
	"net/netip"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// Synthetic 以固定速率产生模拟流量。
type Synthetic struct {
	pps   int
	iface string
	stats atomicStats
}

func NewSynthetic(pps int, iface string) *Synthetic {
	if pps <= 0 {
		pps = 200
	}
	return &Synthetic{pps: pps, iface: iface}
}

// Next 按速率节流后返回一个随机构造的报文。
func (s *Synthetic) Next(ctx context.Context) (*Packet, error) {
	interval := time.Second / time.Duration(s.pps)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	src := netip.AddrFrom4([4]byte{10, byte(rand.Intn(10)), byte(rand.Intn(255)), byte(rand.Intn(254) + 1)})
	dst := netip.AddrFrom4([4]byte{104, 18, byte(rand.Intn(255)), byte(rand.Intn(254) + 1)})
	payload := make([]byte, 64+rand.Intn(900))
	rand.Read(payload)
	var raw []byte
	if rand.Intn(100) < 60 {
		raw = parser.BuildEthIPv4TCP(src, dst, uint16(20000+rand.Intn(40000)), uint16(pick([]int{443, 80, 22, 8080})), payload, false)
	} else {
		raw = parser.BuildEthIPv4UDP(src, dst, uint16(20000+rand.Intn(40000)), 53, payload)
	}
	s.stats.addPacket()
	return &Packet{Ts: time.Now().UTC(), Raw: raw, Iface: s.iface}, nil
}

func (s *Synthetic) Close() error { return nil }
func (s *Synthetic) Stats() Stats { return s.stats.snapshot() }

func pick(a []int) int { return a[rand.Intn(len(a))] }
