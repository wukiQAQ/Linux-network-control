package parser

import (
	"errors"
	"net/netip"
	"testing"
)

func TestParseTCPFrame(t *testing.T) {
	src := netip.MustParseAddr("10.0.0.1")
	dst := netip.MustParseAddr("8.8.8.8")
	raw := BuildEthIPv4TCP(src, dst, 12345, 443, []byte("hello"), true)
	info, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.Protocol != ProtoTCP {
		t.Errorf("Protocol=%d, want %d", info.Protocol, ProtoTCP)
	}
	if info.SrcIP != src || info.DstIP != dst {
		t.Errorf("IP 不匹配: %v -> %v", info.SrcIP, info.DstIP)
	}
	if info.SrcPort != 12345 || info.DstPort != 443 {
		t.Errorf("端口不匹配: %d -> %d", info.SrcPort, info.DstPort)
	}
	if info.TCPFlags&0x02 == 0 {
		t.Error("SYN 标志未解析")
	}
	if info.L4Payload != 5 {
		t.Errorf("L4Payload=%d, want 5", info.L4Payload)
	}
}

func TestParseUDPFrame(t *testing.T) {
	src := netip.MustParseAddr("10.0.0.2")
	dst := netip.MustParseAddr("8.8.8.8")
	raw := BuildEthIPv4UDP(src, dst, 55555, 53, []byte("query123"))
	info, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.Protocol != ProtoUDP || info.SrcPort != 55555 || info.DstPort != 53 {
		t.Errorf("UDP 解析错误: %+v", info)
	}
	if info.L4Payload != 8 {
		t.Errorf("L4Payload=%d, want 8", info.L4Payload)
	}
}

func TestParseICMPHasNoPorts(t *testing.T) {
	src := netip.MustParseAddr("10.0.0.1")
	dst := netip.MustParseAddr("8.8.8.8")
	raw := BuildEthIPv4TCP(src, dst, 0, 0, nil, false)
	raw[23] = ProtoICMP // 把 IPv4 protocol 字段改为 ICMP
	info, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.Protocol != ProtoICMP {
		t.Errorf("Protocol=%d, want ICMP", info.Protocol)
	}
	if info.SrcPort != 0 || info.DstPort != 0 {
		t.Errorf("ICMP 不应有端口: %d -> %d", info.SrcPort, info.DstPort)
	}
}

func TestParseShortPacket(t *testing.T) {
	if _, err := Parse([]byte{1, 2, 3}); !errors.Is(err, ErrShort) {
		t.Errorf("期望 ErrShort，得到 %v", err)
	}
}

func TestParseUnsupportedEtherType(t *testing.T) {
	// 14 字节以太网头，EtherType = IPv6
	raw := make([]byte, 14)
	raw[12], raw[13] = 0x86, 0xdd
	if _, err := Parse(raw); !errors.Is(err, ErrUnsupported) {
		t.Errorf("期望 ErrUnsupported，得到 %v", err)
	}
}
