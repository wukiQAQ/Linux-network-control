package filter

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// runProgram 是一个最小经典 BPF 解释器：只实现本包生成的指令子集，
// 语义与内核一致（**越界 load 直接返回 0 = 丢弃**）。
// 有了它，内核程序的行为就能在离线环境（Windows）被真实验证，而不是"写完就发"。
func runProgram(prog Program, frame []byte) uint32 {
	var a uint32
	for pc, steps := 0, 0; steps < 4096; steps++ {
		if pc < 0 || pc >= len(prog) {
			return 0
		}
		ins := prog[pc]
		switch ins.Code {
		case opLdW:
			v, ok := loadN(frame, int(ins.K), 4)
			if !ok {
				return 0
			}
			a = v
		case opLdH:
			v, ok := loadN(frame, int(ins.K), 2)
			if !ok {
				return 0
			}
			a = v
		case opLdB:
			v, ok := loadN(frame, int(ins.K), 1)
			if !ok {
				return 0
			}
			a = v
		case opLdLen:
			a = uint32(len(frame))
		case opAndK:
			a &= ins.K
		case opJeqK:
			if a == ins.K {
				pc += 1 + int(ins.JT)
			} else {
				pc += 1 + int(ins.JF)
			}
			continue
		case opJgeK:
			if a >= ins.K {
				pc += 1 + int(ins.JT)
			} else {
				pc += 1 + int(ins.JF)
			}
			continue
		case opJa:
			pc += 1 + int(ins.K)
			continue
		case opRetK:
			return ins.K
		default:
			return 0
		}
		pc++
	}
	return 0
}

func loadN(b []byte, off, n int) (uint32, bool) {
	if off < 0 || off+n > len(b) || n < 1 || n > 4 {
		return 0, false
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<8 | uint32(b[off+i])
	}
	return v, true
}

// ipv4Fragment 构造一个 IPv4 分片（非首片：没有传输层头）。
func ipv4Fragment() []byte {
	src := netip.MustParseAddr("10.0.0.5").As4()
	dst := netip.MustParseAddr("8.8.8.8").As4()
	ip := make([]byte, 28)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)))
	binary.BigEndian.PutUint16(ip[6:], 0x00b9) // 分片偏移（非首片）
	ip[9] = parser.ProtoTCP
	copy(ip[12:16], src[:])
	copy(ip[16:20], dst[:])
	return append(ethHeader(parser.EthTypeIPv4), ip...)
}

func corpus() map[string][]byte {
	return map[string][]byte{
		"tcp443":    frameIPv4TCP("10.0.0.5", "93.184.216.34", 51000, 443),
		"udp53":     frameIPv4UDP("10.0.0.5", "8.8.8.8", 40000, 53),
		"icmp":      frameIPv4(parser.ProtoICMP, "10.0.0.5", "8.8.8.8", 0, 0),
		"vlan-tcp":  frameVLANIPv4TCP("10.0.0.5", "10.0.0.9", 40000, 443),
		"arp":       frameARP(),
		"ipv6-tcp":  frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 40000, 443, false),
		"ipv6-ext":  frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 40000, 443, true),
		"ipv6-udp":  frameIPv6(parser.ProtoUDP, "2001:db8::1", "2001:db8::9", 40000, 53, false),
		"fragment":  ipv4Fragment(),
		"short":     {0x00, 0x01, 0x02},
		"eth-trunc": {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x08, 0x00},
	}
}

var kernelExprs = []string{
	"ip", "ip6", "arp", "tcp", "udp", "icmp", "icmp6",
	"host 10.0.0.5", "src host 10.0.0.5", "dst host 93.184.216.34", "host 8.8.8.8",
	"host 2001:db8::1", "src host 2001:db8::1", "dst host 2001:db8::2", "host 2001:db8::9",
	"net 10.0.0.0/8", "net 192.168.0.0/16", "net 93.184.216.0/24", "net 2001:db8::/32",
	"port 443", "port 53", "portrange 40000-41000", "dst port 443",
	"tcp and host 10.0.0.5", "tcp or udp", "ip and not port 443",
	"host 10.0.0.5 or host 10.0.0.9", "host 10.0.0.5 and not port 22",
	"(tcp and dst port 443) or host 2001:db8::1", "not tcp", "not ip", "not (tcp or udp)",
}

// TestKernelProgramIsSuperset 是内核剪枝的核心不变式测试：
// 凡是用户态会命中的帧，内核程序必须放行（宁可多放行，绝不误丢）。
func TestKernelProgramIsSuperset(t *testing.T) {
	frames := corpus()
	checked := 0
	for _, expr := range kernelExprs {
		flt := mustParse(t, expr)
		prog, ok := flt.KernelProgram()
		if !ok {
			continue // 无剪枝价值（全部放行），由用户态精确判定
		}
		if len(prog) > maxProgram {
			t.Errorf("%q 生成的指令数 %d 超过内核上限 %d", expr, len(prog), maxProgram)
		}
		checked++
		for name, raw := range frames {
			if flt.Match(raw) && runProgram(prog, raw) == 0 {
				t.Errorf("违反超集不变式：expr=%q frame=%s 用户态命中，但内核程序会丢弃", expr, name)
			}
		}
	}
	if checked < 20 {
		t.Errorf("只有 %d 条表达式生成了剪枝程序，覆盖面不足", checked)
	}
}

// TestKernelProgramPrunes 验证内核程序确实在剪枝（不是退化成全部放行）。
func TestKernelProgramPrunes(t *testing.T) {
	cases := []struct {
		expr  string
		frame []byte
		want  bool
	}{
		{"ip", frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false), false},
		{"ip", frameIPv4TCP("10.0.0.5", "10.0.0.9", 1, 2), true},
		{"ip6", frameIPv4TCP("10.0.0.5", "10.0.0.9", 1, 2), false},
		{"arp", frameIPv4TCP("10.0.0.5", "10.0.0.9", 1, 2), false},
		{"tcp", frameIPv4UDP("10.0.0.5", "10.0.0.9", 1, 2), false},
		{"tcp", frameIPv4TCP("10.0.0.5", "10.0.0.9", 1, 2), true},
		{"tcp", frameIPv6(parser.ProtoUDP, "2001:db8::1", "2001:db8::2", 1, 2, false), true}, // IPv6 保守放行
		{"udp", frameIPv4(parser.ProtoICMP, "10.0.0.5", "10.0.0.9", 0, 0), false},
		{"host 10.0.0.5", frameIPv4TCP("10.0.0.9", "8.8.8.8", 1, 2), false},
		{"host 10.0.0.5", frameIPv4TCP("10.0.0.5", "8.8.8.8", 1, 2), true},
		{"host 10.0.0.5", frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false), false},
		{"net 192.168.0.0/16", frameIPv4TCP("10.0.0.5", "8.8.8.8", 1, 2), false},
		{"net 10.0.0.0/8", frameIPv4TCP("10.1.2.3", "8.8.8.8", 1, 2), true},
		{"port 443", frameIPv4(parser.ProtoICMP, "10.0.0.5", "8.8.8.8", 0, 0), false},
		{"port 443", frameIPv4TCP("10.0.0.5", "8.8.8.8", 1, 443), true},
		{"host 2001:db8::1", frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false), true},
		{"host 2001:db8::1", frameIPv6(parser.ProtoTCP, "2001:db8::9", "2001:db8::2", 1, 2, false), false},
		{"host 2001:db8::1", frameIPv4TCP("10.0.0.5", "8.8.8.8", 1, 2), false},
		{"net 2001:db8::/32", frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false), true},
		{"net 2001:db9::/32", frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false), false},
		{"tcp and host 10.0.0.5", frameIPv4TCP("10.0.0.5", "8.8.8.8", 1, 2), true},
		{"tcp and host 10.0.0.5", frameIPv4UDP("10.0.0.5", "8.8.8.8", 1, 2), false}, // 主机命中但不是 TCP
	}
	for _, c := range cases {
		prog, ok := mustParse(t, c.expr).KernelProgram()
		if !ok {
			t.Fatalf("%q 应该能生成剪枝程序", c.expr)
		}
		if got := runProgram(prog, c.frame) != 0; got != c.want {
			t.Errorf("%q 对帧的判定 = %v, want %v", c.expr, got, c.want)
		}
	}

	// VLAN 帧：内核不解析 VLAN，必须保守放行
	for _, expr := range []string{"tcp", "ip", "host 10.0.0.5", "port 443", "net 10.0.0.0/8"} {
		prog, ok := mustParse(t, expr).KernelProgram()
		if !ok {
			t.Fatalf("%q 应该能生成剪枝程序", expr)
		}
		if runProgram(prog, frameVLANIPv4TCP("10.0.0.5", "10.0.0.9", 1, 2)) == 0 {
			t.Errorf("%q 对 VLAN 帧应保守放行", expr)
		}
	}
}

// TestKernelProgramFallsBack 校验"没有剪枝价值"与 nil 的情况。
func TestKernelProgramFallsBack(t *testing.T) {
	for _, expr := range []string{"not tcp", "not ip", "host 10.0.0.5 or not ip", "not (tcp or udp)"} {
		prog, ok := mustParse(t, expr).KernelProgram()
		if ok {
			t.Errorf("%q 会退化为全部放行，不应下发内核程序（实际 %d 条指令）", expr, len(prog))
		}
	}
	var nilFilter *Filter
	if prog, ok := nilFilter.KernelProgram(); ok || prog != nil {
		t.Error("nil 过滤器不应生成内核程序")
	}
}

// TestKernelProgramLenCheckPrefix 校验长度检查前缀：短帧一律放行（否则内核会因越界读直接丢包）。
func TestKernelProgramLenCheckPrefix(t *testing.T) {
	prog, ok := mustParse(t, "host 2001:db8::1").KernelProgram()
	if !ok {
		t.Fatal("应生成内核程序")
	}
	if prog[0].Code != opLdLen {
		t.Fatalf("程序开头应是帧长检查，实际 opcode=0x%02x", prog[0].Code)
	}
	short := make([]byte, 20)
	if runProgram(prog, short) == 0 {
		t.Error("帧长不足时应放行（交给用户态精确判定）")
	}
	if runProgram(prog, frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 1, 2, false)) == 0 {
		t.Error("正常长度的命中帧不应被丢弃")
	}
}
