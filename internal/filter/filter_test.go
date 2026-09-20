package filter

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// ---------- 报文构造 ----------

func ethHeader(etype uint16) []byte {
	h := make([]byte, 14)
	copy(h[0:6], []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01})
	copy(h[6:12], []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02})
	binary.BigEndian.PutUint16(h[12:], etype)
	return h
}

// l4Header 生成传输层头：TCP 20 字节、其余（UDP/ICMP）8 字节，端口写在前 4 字节。
func l4Header(proto uint8, sport, dport uint16) []byte {
	n := 8
	if proto == parser.ProtoTCP {
		n = 20
	}
	h := make([]byte, n)
	binary.BigEndian.PutUint16(h[0:], sport)
	binary.BigEndian.PutUint16(h[2:], dport)
	return h
}

// frameIPv4TCP / frameIPv4UDP 复用解析器自带的构造器，
// 保证"过滤器语义"与"解析器语义"面对的是同一批报文。
func frameIPv4TCP(src, dst string, sport, dport uint16) []byte {
	return parser.BuildEthIPv4TCP(netip.MustParseAddr(src), netip.MustParseAddr(dst), sport, dport, []byte("payload"), false)
}

func frameIPv4UDP(src, dst string, sport, dport uint16) []byte {
	return parser.BuildEthIPv4UDP(netip.MustParseAddr(src), netip.MustParseAddr(dst), sport, dport, []byte("payload"))
}

// frameIPv4 构造 以太网 + IPv4 + 传输层 帧（用于 ICMP 等没有端口的协议）。
func frameIPv4(proto uint8, src, dst string, sport, dport uint16) []byte {
	src4 := netip.MustParseAddr(src).As4()
	dst4 := netip.MustParseAddr(dst).As4()
	l4 := l4Header(proto, sport, dport)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)+len(l4)))
	ip[8] = 64
	ip[9] = proto
	copy(ip[12:16], src4[:])
	copy(ip[16:20], dst4[:])
	frame := append(ethHeader(parser.EthTypeIPv4), ip...)
	return append(frame, l4...)
}

// frameIPv6 构造 以太网 + IPv6 + 传输层 帧；ext 为 true 时插入一个逐跳扩展头。
func frameIPv6(proto uint8, src, dst string, sport, dport uint16, ext bool) []byte {
	src16 := netip.MustParseAddr(src).As16()
	dst16 := netip.MustParseAddr(dst).As16()
	l4 := l4Header(proto, sport, dport)
	next := proto
	var extHdr []byte
	if ext {
		extHdr = make([]byte, 8)
		extHdr[0] = proto // 扩展头指向真正的传输层协议
		extHdr[1] = 0     // 长度：(0+1)*8 = 8 字节
		next = 0          // 逐跳扩展头
	}
	ip := make([]byte, 40)
	ip[0] = 0x60
	binary.BigEndian.PutUint16(ip[4:], uint16(len(extHdr)+len(l4)))
	ip[6] = next
	ip[7] = 64
	copy(ip[8:24], src16[:])
	copy(ip[24:40], dst16[:])
	frame := append(ethHeader(parser.EthTypeIPv6), ip...)
	frame = append(frame, extHdr...)
	return append(frame, l4...)
}

func frameARP() []byte { return append(ethHeader(parser.EthTypeARP), make([]byte, 28)...) }

// frameVLANIPv4TCP 构造带 VLAN 标签的 IPv4/TCP 帧。
func frameVLANIPv4TCP(src, dst string, sport, dport uint16) []byte {
	inner := frameIPv4TCP(src, dst, sport, dport)
	frame := append(ethHeader(parser.EthTypeVLAN), 0x00, 0x0a) // TCI
	return append(frame, inner[12:]...)
}

func mustParse(t *testing.T, expr string) *Filter {
	t.Helper()
	f, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q) 报错: %v", expr, err)
	}
	if f == nil {
		t.Fatalf("Parse(%q) 返回了空过滤器，但表达式非空", expr)
	}
	return f
}

// ---------- 解析 ----------

func TestParseEmptyMeansNoFilter(t *testing.T) {
	for _, expr := range []string{"", "   ", "\t\n"} {
		f, err := Parse(expr)
		if err != nil || f != nil {
			t.Fatalf("Parse(%q) = (%v, %v)，期望 (nil, nil)", expr, f, err)
		}
	}
	var nilFilter *Filter
	if !nilFilter.Match(frameIPv4TCP("10.0.0.1", "10.0.0.2", 1, 2)) {
		t.Error("nil 过滤器应视为全部命中")
	}
	if nilFilter.String() != "" {
		t.Errorf("nil 过滤器 String() = %q，期望空串", nilFilter.String())
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"vlan", "暂不支持"},
		{"host", "缺少参数"},
		{"port 0", "不是合法端口"},
		{"port 70000", "不是合法端口"},
		{"host 999.1.1.1", "不是合法的 IP"},
		{"src 10.0.0.1", "只支持"},
		{"tcp and", "缺少条件"},
		{"(tcp", "缺少右括号"},
		{"tcp)", "无法解析"},
		{"portrange 200-100", "起止顺序"},
		{"portrange 100", "portrange 要写成"},
		{"foo", "无法识别的写法"},
		{"tcp & udp", "逻辑运算符要写成"},
		{"net 10.0.0.0/33", "不是合法的网段"},
	}
	for _, c := range cases {
		_, err := Parse(c.expr)
		if err == nil {
			t.Errorf("Parse(%q) 期望报错，实际通过", c.expr)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) 错误 %q 不含 %q", c.expr, err.Error(), c.want)
		}
	}
}

func TestParseNormalizesText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"TCP AND PORT 443", "tcp and port 443"},
		{"ipv6", "ip6"},
		{"src host 10.0.0.5", "src host 10.0.0.5"},
		{"dst net 10.0.0.0/8", "dst net 10.0.0.0/8"},
		{"net 10.0.0.5", "net 10.0.0.5/32"},
		{"!tcp", "not tcp"},
		{"tcp&&udp", "tcp and udp"},
		{"tcp || (udp and port 53)", "tcp or (udp and port 53)"},
		{"(tcp or udp) and port 53", "(tcp or udp) and port 53"},
		{"not (tcp or udp)", "not (tcp or udp)"},
		{"portrange 1000-2000", "portrange 1000-2000"},
	}
	for _, c := range cases {
		if got := mustParse(t, c.in).String(); got != c.want {
			t.Errorf("Parse(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------- 匹配 ----------

func TestMatchIPv4(t *testing.T) {
	frames := []struct {
		name string
		raw  []byte
		want map[string]bool
	}{
		{
			name: "TCP/443",
			raw:  frameIPv4TCP("10.0.0.5", "93.184.216.34", 51000, 443),
			want: map[string]bool{
				"ip": true, "ip6": false, "arp": false, "tcp": true, "udp": false, "icmp": false,
				"port 443": true, "dst port 443": true, "src port 443": false,
				"portrange 50000-52000": true, "portrange 100-200": false,
				"host 10.0.0.5": true, "src host 10.0.0.5": true, "dst host 10.0.0.5": false,
				"host 93.184.216.34": true, "net 10.0.0.0/8": true, "net 192.168.0.0/16": false,
				"not udp": true, "tcp and dst port 443": true, "tcp and dst port 80": false,
				"host 10.0.0.5 or host 10.0.0.6": true, "not ip": false,
			},
		},
		{
			name: "UDP/53",
			raw:  frameIPv4UDP("10.0.0.5", "8.8.8.8", 40000, 53),
			want: map[string]bool{
				"tcp": false, "udp": true, "port 53": true, "dst port 443": false,
				"ip and not port 443": true, "ip and not port 53": false,
			},
		},
		{
			name: "ICMP",
			raw:  frameIPv4(parser.ProtoICMP, "10.0.0.5", "8.8.8.8", 0, 0),
			want: map[string]bool{
				"icmp": true, "tcp": false, "udp": false, "ip": true, "port 80": false,
			},
		},
	}
	for _, f := range frames {
		for expr, want := range f.want {
			if got := mustParse(t, expr).Match(f.raw); got != want {
				t.Errorf("%s: Match(%q) = %v, want %v", f.name, expr, got, want)
			}
		}
	}
}

// TestMatchIPv6 覆盖 IPv6 与带扩展头的报文：过滤语义与解析器一致，扩展头会被跳过。
func TestMatchIPv6(t *testing.T) {
	plain := frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 40000, 443, false)
	withExt := frameIPv6(parser.ProtoTCP, "2001:db8::1", "2001:db8::2", 40000, 443, true)
	udp6 := frameIPv6(parser.ProtoUDP, "2001:db8::1", "2001:db8::9", 40000, 53, false)
	cases := []struct {
		expr string
		hit  bool
	}{
		{"ip6", true},
		{"ip", false},
		{"tcp", true},
		{"udp", false},
		{"port 443", true},
		{"dst port 443", true},
		{"host 2001:db8::1", true},
		{"src host 2001:db8::1", true},
		{"dst host 2001:db8::2", true},
		{"host 2001:db8::9", false},
		{"net 2001:db8::/32", true},
		{"net 2001:db9::/32", false},
		{"host 10.0.0.1", false}, // 地址族不同
		{"not ip6", false},
	}
	for _, raw := range [][]byte{plain, withExt} {
		for _, c := range cases {
			if got := mustParse(t, c.expr).Match(raw); got != c.hit {
				t.Errorf("Match(%q) = %v, want %v（报文 %d 字节）", c.expr, got, c.hit, len(raw))
			}
		}
	}
	for _, c := range []struct {
		expr string
		hit  bool
	}{
		{"udp", true}, {"tcp", false}, {"port 53", true}, {"host 2001:db8::9", true},
	} {
		if got := mustParse(t, c.expr).Match(udp6); got != c.hit {
			t.Errorf("UDP6: Match(%q) = %v, want %v", c.expr, got, c.hit)
		}
	}
}

// TestMatchARPAndVLAN：ARP 只看链路层类型；VLAN 标签按解析器语义被跳过。
func TestMatchARPAndVLAN(t *testing.T) {
	arp := frameARP()
	if !mustParse(t, "arp").Match(arp) {
		t.Error("arp 应命中 ARP 帧")
	}
	for _, expr := range []string{"ip", "ip6", "tcp", "port 80", "host 10.0.0.1"} {
		if mustParse(t, expr).Match(arp) {
			t.Errorf("%q 不应命中 ARP 帧", expr)
		}
	}
	vlan := frameVLANIPv4TCP("10.0.0.5", "10.0.0.9", 1234, 443)
	for _, expr := range []string{"ip", "tcp", "dst port 443", "host 10.0.0.5"} {
		if !mustParse(t, expr).Match(vlan) {
			t.Errorf("%q 应命中带 VLAN 标签的 IPv4/TCP 帧", expr)
		}
	}
}

// TestMatchShortFrame：过短的帧不应该命中任何 IP 层条件，也不该 panic。
func TestMatchShortFrame(t *testing.T) {
	short := []byte{0x00, 0x01, 0x02}
	for _, expr := range []string{"ip", "arp", "tcp", "udp", "host 10.0.0.1", "port 80", "net 10.0.0.0/8"} {
		if mustParse(t, expr).Match(short) {
			t.Errorf("%q 不应命中过短的帧", expr)
		}
	}
	if !mustParse(t, "not ip").Match(short) {
		t.Error("not ip 应命中非 IP 帧")
	}
}
