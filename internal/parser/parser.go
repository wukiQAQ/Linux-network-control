// Package parser 实现链路层/网络层/传输层的协议解码。
// MVP 范围：以太网（含单层 VLAN）、IPv4、TCP/UDP/ICMP。
// 该包同时提供测试与 synthetic 数据源使用的报文构造器（Builder）。
package parser

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	ProtoICMP = 1
	ProtoTCP  = 6
	ProtoUDP  = 17

	EthTypeIPv4  = 0x0800
	EthTypeVLAN  = 0x8100
	EthTypeQinQ  = 0x88a8
	ethHdrLen    = 14
	ipv4HdrMin   = 20
	tcpHdrMin    = 20
	udpHdrMin    = 8
)

// Info 是解析器输出的"包级摘要"，不含原始字节，供流表与聚合使用。
type Info struct {
	Protocol   uint8
	SrcIP      netip.Addr
	DstIP      netip.Addr
	SrcPort    uint16
	DstPort    uint16
	TCPFlags   uint8 // TCP 标志位（仅 TCP 有意义）
	L4Payload  int   // 传输层负载长度
}

var (
	ErrShort       = errors.New("报文长度不足")
	ErrUnsupported = errors.New("不支持的报文类型")
)

// Parse 解码一个完整以太网帧。只做"剥头提取元数据"，不做校验和验证（内核已校验）。
func Parse(b []byte) (*Info, error) {
	if len(b) < ethHdrLen {
		return nil, ErrShort
	}
	// 跳过 VLAN tag（支持单层与 QinQ），读取真正的 EtherType。
	off := 12
	etype := int(binary.BigEndian.Uint16(b[off:]))
	for etype == EthTypeVLAN || etype == EthTypeQinQ {
		off += 4
		if len(b) < off+2 {
			return nil, ErrShort
		}
		etype = int(binary.BigEndian.Uint16(b[off:]))
	}
	if etype != EthTypeIPv4 {
		return nil, fmt.Errorf("%w: ethertype=0x%04x", ErrUnsupported, etype)
	}
	l3 := off + 2 // IP 层起点：以太网头 14 字节 = ethertype 字段之后
	if len(b) < l3+ipv4HdrMin {
		return nil, ErrShort
	}
	verIHL := b[l3]
	if verIHL>>4 != 4 {
		return nil, fmt.Errorf("%w: IP 版本=%d（MVP 仅支持 IPv4）", ErrUnsupported, verIHL>>4)
	}
	ihl := int(verIHL&0x0f) * 4
	if ihl < ipv4HdrMin || len(b) < l3+ihl {
		return nil, ErrShort
	}
	info := &Info{
		Protocol:  b[l3+9],
			SrcIP:     netip.AddrFrom4([4]byte{b[l3+12], b[l3+13], b[l3+14], b[l3+15]}),
			DstIP:     netip.AddrFrom4([4]byte{b[l3+16], b[l3+17], b[l3+18], b[l3+19]}),
			L4Payload: len(b) - (l3 + ihl),
	}
	l4 := l3 + ihl
	switch info.Protocol {
	case ProtoTCP:
		if len(b) < l4+tcpHdrMin {
			return nil, ErrShort
		}
		info.SrcPort = binary.BigEndian.Uint16(b[l4:])
		info.DstPort = binary.BigEndian.Uint16(b[l4+2:])
		info.TCPFlags = b[l4+13]
		doff := int(b[l4+12]>>4) * 4 // 数据偏移 = TCP 头部总长（含选项）
		if doff < tcpHdrMin {
			doff = tcpHdrMin
		}
		if l4+doff <= len(b) {
			info.L4Payload = len(b) - (l4 + doff)
		}
	case ProtoUDP:
		if len(b) < l4+udpHdrMin {
			return nil, ErrShort
		}
		info.SrcPort = binary.BigEndian.Uint16(b[l4:])
		info.DstPort = binary.BigEndian.Uint16(b[l4+2:])
		info.L4Payload -= udpHdrMin
	case ProtoICMP:
		// ICMP 无端口，保持 0
	default:
		// 其他传输层协议（如 GRE/ESP）仅保留 IP 层信息
	}
	return info, nil
}

// BuildEthIPv4TCP 构造一个以太网 + IPv4 + TCP 报文（校验和为 0，解析层不校验）。
func BuildEthIPv4TCP(src, dst netip.Addr, sport, dport uint16, payload []byte, syn bool) []byte {
	flags := uint16(0x10) // ACK
	if syn {
		flags = 0x02
	}
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:], sport)
	binary.BigEndian.PutUint16(tcp[2:], dport)
	binary.BigEndian.PutUint32(tcp[4:], 1) // seq
	binary.BigEndian.PutUint16(tcp[12:], 0x5000|flags)
	binary.BigEndian.PutUint16(tcp[14:], 65535) // window
	tcp = append(tcp, payload...)
	return buildEthIPv4(src, dst, ProtoTCP, tcp)
}

// BuildEthIPv4UDP 构造一个以太网 + IPv4 + UDP 报文（校验和为 0）。
func BuildEthIPv4UDP(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:], sport)
	binary.BigEndian.PutUint16(udp[2:], dport)
	binary.BigEndian.PutUint16(udp[4:], uint16(8+len(payload)))
	udp = append(udp, payload...)
	return buildEthIPv4(src, dst, ProtoUDP, udp)
}

func buildEthIPv4(src, dst netip.Addr, proto uint8, l4 []byte) []byte {
	s4, d4 := src.As4(), dst.As4()
	total := 20 + len(l4)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(total))
	binary.BigEndian.PutUint16(ip[4:], 0x1234) // ID
	ip[8] = 64                                  // TTL
	ip[9] = proto
	copy(ip[12:16], s4[:])
	copy(ip[16:20], d4[:])

	frame := make([]byte, 0, ethHdrLen+len(ip)+len(l4))
	frame = append(frame,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // dst MAC
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, // src MAC
		0x08, 0x00, // EtherType IPv4
	)
	frame = append(frame, ip...)
	frame = append(frame, l4...)
	return frame
}
