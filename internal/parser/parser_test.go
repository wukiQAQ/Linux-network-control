package parser

import (
	"encoding/binary"
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
	raw[12], raw[13] = 0x08, 0x06
	if _, err := Parse(raw); !errors.Is(err, ErrUnsupported) {
		t.Errorf("期望 ErrUnsupported，得到 %v", err)
	}
}

// buildIPv6 构造一个最小的以太网 + IPv6 + L4 报文（测试用，校验和留 0）。
func buildIPv6(src, dst netip.Addr, next uint8, l4 []byte) []byte {
	frame := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x86, 0xdd}
	ip := make([]byte, 40)
	ip[0] = 0x60
	binary.BigEndian.PutUint16(ip[4:6], uint16(len(l4)))
	ip[6] = next
	ip[7] = 64
	s16 := src.As16()
	d16 := dst.As16()
	copy(ip[8:24], s16[:])
	copy(ip[24:40], d16[:])
	return append(append(frame, ip...), l4...)
}

func TestParseIPv6TCP(t *testing.T) {
	src := netip.MustParseAddr("2400:3200::1")
	dst := netip.MustParseAddr("2001:db8::2")
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:2], 12345)
	binary.BigEndian.PutUint16(tcp[2:4], 443)
	tcp[12] = 0x50
	tcp[13] = 0x02
	tcp = append(tcp, []byte("hello")...)

	info, err := Parse(buildIPv6(src, dst, ProtoTCP, tcp))
	if err != nil {
		t.Fatalf("解析 IPv6/TCP 失败: %v", err)
	}
	if !info.IPv6 {
		t.Error("应标记为 IPv6 报文")
	}
	if info.SrcIP != src || info.DstIP != dst {
		t.Errorf("地址解析错误: %v -> %v", info.SrcIP, info.DstIP)
	}
	if info.SrcPort != 12345 || info.DstPort != 443 {
		t.Errorf("端口解析错误: %d -> %d", info.SrcPort, info.DstPort)
	}
	if info.Protocol != ProtoTCP || info.TCPFlags != 0x02 {
		t.Errorf("协议/标志位错误: proto=%d flags=%#x", info.Protocol, info.TCPFlags)
	}
	if info.L4Payload != 5 {
		t.Errorf("负载长度应为 5，实际 %d", info.L4Payload)
	}
}

func TestParseIPv6UDPAndICMPv6(t *testing.T) {
	src := netip.MustParseAddr("fe80::1")
	dst := netip.MustParseAddr("fe80::2")

	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:2], 5353)
	binary.BigEndian.PutUint16(udp[2:4], 5353)
	binary.BigEndian.PutUint16(udp[4:6], 11)
	udp = append(udp, []byte("dns")...)
	info, err := Parse(buildIPv6(src, dst, ProtoUDP, udp))
	if err != nil {
		t.Fatalf("解析 IPv6/UDP 失败: %v", err)
	}
	if info.Protocol != ProtoUDP || info.SrcPort != 5353 || info.L4Payload != 3 {
		t.Errorf("IPv6/UDP 解析错误: %+v", info)
	}

	// ICMPv6：无端口
	icmp := []byte{0x80, 0x00, 0x00, 0x00}
	info, err = Parse(buildIPv6(src, dst, ProtoICMPv6, icmp))
	if err != nil {
		t.Fatalf("解析 ICMPv6 失败: %v", err)
	}
	if info.Protocol != ProtoICMPv6 || info.SrcPort != 0 || info.DstPort != 0 {
		t.Errorf("ICMPv6 解析错误: %+v", info)
	}
}

func TestParseIPv6ExtensionHeader(t *testing.T) {
	src := netip.MustParseAddr("2001:db8:1::1")
	dst := netip.MustParseAddr("2001:db8:2::2")

	// 逐跳扩展头（next=UDP，长度 (0+1)*8 = 8 字节）后接 UDP
	hop := make([]byte, 8)
	hop[0] = ProtoUDP
	hop[1] = 0
	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:2], 10000)
	binary.BigEndian.PutUint16(udp[2:4], 20000)
	payload := append(append(hop, udp...), []byte("x")...)

	info, err := Parse(buildIPv6(src, dst, 0, payload))
	if err != nil {
		t.Fatalf("解析带扩展头的 IPv6 失败: %v", err)
	}
	if info.Protocol != ProtoUDP || info.SrcPort != 10000 || info.DstPort != 20000 {
		t.Errorf("扩展头跳过后解析错误: %+v", info)
	}
	if info.L4Payload != 1 {
		t.Errorf("负载长度应为 1（扩展头不计数），实际 %d", info.L4Payload)
	}
}
