package capture

import (
	"context"
	"io"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

func TestPcapRoundtrip(t *testing.T) {
	src := netip.MustParseAddr("10.0.0.1")
	dst := netip.MustParseAddr("8.8.8.8")
	raw1 := parser.BuildEthIPv4TCP(src, dst, 1000, 443, []byte("a"), true)
	raw2 := parser.BuildEthIPv4UDP(src, dst, 1000, 53, []byte("b"))
	t0 := time.Unix(1700000000, 0).UTC()
	t1 := time.Unix(1700000001, 0).UTC()
	path := t.TempDir() + "/sample.pcap"
	if err := WritePCAP(path, []Packet{{Ts: t0, Raw: raw1}, {Ts: t1, Raw: raw2}}); err != nil {
		t.Fatalf("WritePCAP: %v", err)
	}
	r, err := OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer r.Close()
	ctx := context.Background()
	p1, err := r.Next(ctx)
	if err != nil {
		t.Fatalf("Next #1: %v", err)
	}
	if string(p1.Raw) != string(raw1) || p1.Ts.Unix() != t0.Unix() {
		t.Error("第一个报文不一致")
	}
	p2, _ := r.Next(ctx)
	if string(p2.Raw) != string(raw2) || p2.Ts.Unix() != t1.Unix() {
		t.Error("第二个报文不一致")
	}
	if _, err := r.Next(ctx); err != io.EOF {
		t.Errorf("期望 EOF，得到 %v", err)
	}
	if r.Stats().Packets != 2 {
		t.Errorf("Stats.Packets=%d, want 2", r.Stats().Packets)
	}
}

func TestSyntheticCancelRespectsContext(t *testing.T) {
	s := NewSynthetic(1, "eth0") // 1 pps：下次产出需 1s
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.Next(ctx); err == nil {
		t.Error("期望超时错误，实际返回了报文")
	}
}

func TestSyntheticProducesEthernetIPv4(t *testing.T) {
	s := NewSynthetic(20000, "eth0")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	p, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(p.Raw) < 34 {
		t.Fatalf("报文过短: %d", len(p.Raw))
	}
	if p.Raw[12] != 0x08 || p.Raw[13] != 0x00 {
		t.Error("EtherType 不是 IPv4")
	}
	if s.Stats().Packets < 1 {
		t.Error("Stats.Packets 未累计")
	}
}

// TestShouldPollStats 验证内核统计的查询节流：每 128 个包一次。
func TestShouldPollStats(t *testing.T) {
	if shouldPollStats(0) {
		t.Error("第 0 个包不应触发查询")
	}
	if shouldPollStats(1) {
		t.Error("第 1 个包不应触发查询")
	}
	if !shouldPollStats(128) || !shouldPollStats(256) {
		t.Error("每 128 个包应触发一次查询")
	}
	if statsPollEvery != 128 {
		t.Errorf("节流步长应为 128，实际 %d", statsPollEvery)
	}
}

// TestPlanCPUAssignment 校验多队列的 CPU 分配：一核一个、核不够循环复用、无可用 CPU 时不绑定。
func TestPlanCPUAssignment(t *testing.T) {
	cases := []struct {
		name    string
		allowed []int
		n       int
		want    []int
	}{
		{"两队列两核", []int{2, 3, 4, 5}, 2, []int{2, 3}},
		{"四队列四核", []int{2, 3, 4, 5}, 4, []int{2, 3, 4, 5}},
		{"核不够循环复用", []int{2, 3}, 5, []int{2, 3, 2, 3, 2}},
		{"只有一个核", []int{7}, 3, []int{7, 7, 7}},
		{"没有可用 CPU 时不绑定", nil, 2, []int{-1, -1}},
		{"忽略非法 CPU 号", []int{-1, 3}, 2, []int{3, 3}},
		{"零队列", []int{1}, 0, []int{}},
	}
	for _, c := range cases {
		got := planCPUAssignment(c.allowed, c.n)
		if len(got) != len(c.want) {
			t.Fatalf("%s: 长度=%d, want %d", c.name, len(got), len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// TestCPUPlanText 校验绑定日志文案（便于在真机日志里一眼确认绑定结果）。
func TestCPUPlanText(t *testing.T) {
	if got := cpuPlanText([]int{2, 3}); got != "reader0→CPU2 reader1→CPU3" {
		t.Errorf("cpuPlanText = %q", got)
	}
	if got := cpuPlanText([]int{-1}); got != "reader0→不绑定" {
		t.Errorf("cpuPlanText = %q", got)
	}
}

// TestNewLiveWithReadersPinnedUnsupported 校验非 Linux 平台给出明确错误（Linux 上的真实路径由真机验证）。
func TestNewLiveWithReadersPinnedUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux 平台有真实实现，无需此断言")
	}
	if _, err := NewLiveWithReadersPinned("eth0", 2, true); err == nil {
		t.Error("非 Linux 平台应返回错误")
	}
}
