package app

import (
	"context"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/parser"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// TestPcapReplayPipeline 是"回放 → 解析 → 流表 → 聚合 → 存储"的关键链路集成测试。
func TestPcapReplayPipeline(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	aIP := netip.MustParseAddr("10.0.0.1")
	bIP := netip.MustParseAddr("8.8.8.8")
	cIP := netip.MustParseAddr("1.1.1.1")

	pkts := []capture.Packet{
		{Ts: base, Raw: parser.BuildEthIPv4TCP(aIP, bIP, 40000, 443, []byte("a"), true)},
		{Ts: base.Add(time.Second), Raw: parser.BuildEthIPv4TCP(bIP, aIP, 443, 40000, []byte("bb"), false)},
		{Ts: base.Add(2 * time.Second), Raw: parser.BuildEthIPv4TCP(aIP, bIP, 40000, 443, []byte("ccc"), false)},
		{Ts: base.Add(3 * time.Second), Raw: parser.BuildEthIPv4UDP(aIP, cIP, 50000, 53, []byte("dns"))},
	}
	path := t.TempDir() + "/replay.pcap"
	if err := capture.WritePCAP(path, pkts); err != nil {
		t.Fatalf("WritePCAP: %v", err)
	}
	src, err := capture.OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer src.Close()

	cfg := config.Default()
	cfg.MachineID = "m1"
	cfg.Iface = "eth0"
	table := flow.NewTable(30*time.Second, 300*time.Second, nil)
	agg := aggregator.New(100)
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	a := New(cfg, src, table, agg, store)
	ctx := context.Background()
	for {
		p, err := src.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !a.HandlePacket(p) {
			t.Fatalf("报文解析失败: ts=%v", p.Ts)
		}
	}

	// 第一个聚合周期：应产出字节/包统计
	s1 := a.TickOnce(base.Add(5 * time.Second))
	if s1.Bytes == 0 || s1.Bps == 0 {
		t.Errorf("首个采样异常: %+v", s1)
	}
	// 等到空闲超时（30s）触发会话过期与落盘
	a.TickOnce(base.Add(40 * time.Second))

	_, total, err := store.QuerySessions(storage.SessionFilter{}, 10, 0)
	if err != nil || total != 2 {
		t.Fatalf("会话总数=%d err=%v, want 2（A↔B 双向合并为 1 条 + A→C 1 条）", total, err)
	}
	rows, _, _ := store.QuerySessions(storage.SessionFilter{IP: "8.8.8.8"}, 10, 0)
	if len(rows) != 1 {
		t.Fatalf("A↔B 会话数=%d, want 1", len(rows))
	}
	merged := rows[0]
	if merged.Packets != 3 || merged.Bytes == 0 {
		t.Errorf("合并会话统计异常: packets=%d bytes=%d", merged.Packets, merged.Bytes)
	}
	if merged.SrcIP != "10.0.0.1" || merged.SrcPort != 40000 {
		t.Errorf("首次观测方向未保留: %+v", merged)
	}
	pts, _ := store.QueryMetrics("traffic.bps", base, base.Add(40*time.Second))
	if len(pts) != 2 {
		t.Errorf("指标点数=%d, want 2（两次 TickOnce）", len(pts))
	}
}
