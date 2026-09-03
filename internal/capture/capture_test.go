package capture

import (
	"context"
	"io"
	"net/netip"
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
