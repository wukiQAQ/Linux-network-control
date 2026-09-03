package flow

import (
	"net/netip"
	"testing"
	"time"
)

func TestBidirectionalMerge(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	cur := base
	tab := NewTable(30*time.Second, 300*time.Second, func() time.Time { return cur })
	a := netip.MustParseAddr("10.0.0.1")
	b := netip.MustParseAddr("8.8.8.8")

	tab.Handle(6, a, b, 40000, 443, 0x02, 1000, cur)          // A→B SYN
	cur = cur.Add(2 * time.Second)
	tab.Handle(6, b, a, 443, 40000, 0x10, 2000, cur)          // B→A 反向
	cur = cur.Add(2 * time.Second)
	tab.Handle(6, a, b, 40000, 443, 0x10, 1500, cur)          // A→B 继续

	if tab.Active() != 1 {
		t.Fatalf("Active=%d, want 1（双向应归并为一条流）", tab.Active())
	}
	if tab.Created() != 1 {
		t.Fatalf("Created=%d, want 1", tab.Created())
	}
	flows := tab.Snapshot()
	f := flows[0]
	if f.Packets != 3 || f.Bytes != 4500 {
		t.Errorf("Packets=%d Bytes=%d, want 3/4500", f.Packets, f.Bytes)
	}
	want := Key{Src: b, Dst: a, SPort: 443, DPort: 40000, Proto: 6} // 归一化后字典序小的在前
	if f.Key != want {
		t.Errorf("规范 Key=%+v, want %+v", f.Key, want)
	}
	if f.SrcIP != a || f.SPort != 40000 {
		t.Errorf("首次观测方向未保留: src=%v:%d", f.SrcIP, f.SPort)
	}
}
func TestIdleExpire(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	cur := base
	tab := NewTable(30*time.Second, time.Hour, func() time.Time { return cur })
	a := netip.MustParseAddr("10.0.0.1")
	b := netip.MustParseAddr("8.8.8.8")
	tab.Handle(6, a, b, 1, 2, 0, 10, cur)

	cur = cur.Add(31 * time.Second) // 空闲超过 30s
	tab.Handle(17, a, netip.MustParseAddr("1.1.1.1"), 3, 4, 0, 10, cur)
	expired := tab.Expire(cur)
	if len(expired) != 1 {
		t.Fatalf("过期数量=%d, want 1", len(expired))
	}
	if tab.Active() != 1 {
		t.Errorf("Active=%d, want 1（新会话仍活跃）", tab.Active())
	}
}

func TestHardExpire(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	cur := base
	tab := NewTable(time.Hour, 6*time.Second, func() time.Time { return cur })
	a := netip.MustParseAddr("10.0.0.1")
	b := netip.MustParseAddr("8.8.8.8")
	// 每秒都有流量（空闲不超时），但硬超时 6s 到期
	for i := 0; i < 8; i++ {
		tab.Handle(6, a, b, 1, 2, 0, 10, cur)
		cur = cur.Add(time.Second)
	}
	expired := tab.Expire(cur)
	if len(expired) != 1 {
		t.Fatalf("硬超时未生效: expired=%d, want 1", len(expired))
	}
	if tab.Active() != 0 {
		t.Errorf("Active=%d, want 0", tab.Active())
	}
}
