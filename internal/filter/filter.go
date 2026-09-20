// Package filter 实现采集侧过滤：把配置里的过滤表达式解析成 AST，
// 再对每一个以太网帧做匹配（用户态执行，回放 / 演示 / 真实抓包共用同一套语义）。
//
// 语法是 pcap 过滤器的常用子集：
//
//	ip | ip6 | arp | tcp | udp | icmp | icmp6      协议条件
//	[src|dst] host <IPv4|IPv6>                     主机条件
//	[src|dst] net <CIDR>                           网段条件（省略掩码按 /32、/128）
//	[src|dst] port <1-65535>                       单端口条件
//	portrange <起-止>                              端口区间条件
//	not / !、and / &&、or / ||、括号                逻辑组合（not > and > or）
//
// 语义与 internal/parser 保持一致：VLAN 标签（单层与 QinQ）会被跳过，
// IPv6 扩展头（逐跳 / 路由 / 分片 / 目的选项）也会被跳过，
// 所以 "tcp"、"port 443" 对带扩展头的报文同样成立。
// 表达式为空白表示不过滤（返回 nil Filter）。
//
// 本期只在用户态过滤（不匹配的帧在解析前丢弃，省掉后续解析与流表开销）；
// 内核级剪枝（SO_ATTACH_FILTER）需要额外处理 IP 选项与扩展头偏移，列为下一批。
package filter

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// supportedHint 在报错时直接告诉用户"能写什么"，避免只报一句语法错误。
const supportedHint = "支持：ip/ip6/arp/tcp/udp/icmp/icmp6、[src|dst] host <IP>、" +
	"[src|dst] net <CIDR>、[src|dst] port <端口>、portrange <起-止>，以及 and/or/not 与括号"

// Filter 是已解析的过滤表达式；nil 值表示不过滤（所有帧都命中）。
type Filter struct {
	expr string
	root node
}

// Parse 解析过滤表达式；表达式为空白时返回 (nil, nil) 表示不过滤。
func Parse(expr string) (*Filter, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, nil
	}
	toks, err := tokenize(expr)
	if err != nil {
		return nil, err
	}
	p := &parserState{toks: toks}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		t := p.toks[p.pos]
		prev := ""
		if p.pos > 0 {
			prev = p.toks[p.pos-1].text
		}
		return nil, fmt.Errorf("过滤表达式位置 %d：%q 之后的 %q 无法解析（%s）", t.pos+1, prev, t.text, supportedHint)
	}
	return &Filter{expr: expr, root: root}, nil
}

// String 返回规范化后的表达式文本（关键字小写、按优先级补括号）；nil 返回空串。
func (f *Filter) String() string {
	if f == nil {
		return ""
	}
	return f.root.String()
}

// Match 判断一个以太网帧是否命中过滤条件；nil 过滤器视为全部命中。
func (f *Filter) Match(frame []byte) bool {
	if f == nil {
		return true
	}
	return f.root.match(&matcher{frame: frame})
}

// ---------- 词法分析 ----------

// token 是一个词法单元；pos 为它在表达式中的字节位置（用于报错定位）。
type token struct {
	text string
	pos  int
}

// tokenize 把表达式切成括号、逻辑运算符与普通词（地址、端口、关键字）。
func tokenize(expr string) ([]token, error) {
	const seps = " \t\r\n()&|!"
	var out []token
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case strings.IndexByte(" \t\r\n", c) >= 0:
			i++
		case c == '(' || c == ')' || c == '!':
			out = append(out, token{text: string(c), pos: i})
			i++
		case c == '&' || c == '|':
			if i+1 >= len(expr) || expr[i+1] != c {
				return nil, fmt.Errorf("过滤表达式位置 %d：逻辑运算符要写成 %q", i+1, strings.Repeat(string(c), 2))
			}
			out = append(out, token{text: string(c) + string(c), pos: i})
			i += 2
		default:
			start := i
			for i < len(expr) && strings.IndexByte(seps, expr[i]) < 0 {
				i++
			}
			out = append(out, token{text: expr[start:i], pos: start})
		}
	}
	return out, nil
}

// ---------- 语法树 ----------

// dir 表示条件的匹配方向：任意一侧 / 源 / 目的。
type dir uint8

const (
	dirAny dir = iota
	dirSrc
	dirDst
)

func (d dir) prefix() string {
	switch d {
	case dirSrc:
		return "src "
	case dirDst:
		return "dst "
	}
	return ""
}

type node interface {
	String() string
	match(m *matcher) bool
}

// protoNode 是协议条件：ip / ip6 / arp / tcp / udp / icmp / icmp6。
type protoNode struct{ name string }

// hostNode 是主机条件：[src|dst] host <IP>。
type hostNode struct {
	dir  dir
	addr netip.Addr
}

// netNode 是网段条件：[src|dst] net <CIDR>。
type netNode struct {
	dir dir
	pfx netip.Prefix
}

// portNode 是端口条件：[src|dst] port <端口> 或 portrange <起-止>。
type portNode struct {
	dir    dir
	lo, hi uint16
}

type notNode struct{ x node }
type andNode struct{ l, r node }
type orNode struct{ l, r node }

var protoKeywords = map[string]string{
	"ip": "ip", "ip4": "ip", "ipv4": "ip",
	"ip6": "ip6", "ipv6": "ip6",
	"arp": "arp",
	"tcp": "tcp", "udp": "udp",
	"icmp":  "icmp",
	"icmp6": "icmp6", "icmpv6": "icmp6",
}

// unsupportedPrimitives 是 pcap 里常见但本实现暂不支持的原语，报错时单独说明。
var unsupportedPrimitives = map[string]bool{
	"vlan": true, "ether": true, "wlan": true, "broadcast": true, "multicast": true,
	"gateway": true, "proto": true, "protochain": true, "len": true, "greater": true,
	"less": true, "inbound": true, "outbound": true, "srcmask": true, "dstmask": true,
}

// ---------- 语法分析 ----------

type parserState struct {
	toks []token
	pos  int
}

func (p *parserState) peek() (token, bool) {
	if p.pos >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.pos], true
}

func (p *parserState) next() (token, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

func isAnd(s string) bool { return s == "and" || s == "&&" }
func isOr(s string) bool  { return s == "or" || s == "||" }

func (p *parserState) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || !isOr(strings.ToLower(t.text)) {
			return left, nil
		}
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{l: left, r: right}
	}
}

func (p *parserState) parseAnd() (node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || !isAnd(strings.ToLower(t.text)) {
			return left, nil
		}
		p.pos++
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andNode{l: left, r: right}
	}
}

func (p *parserState) parseNot() (node, error) {
	if t, ok := p.peek(); ok {
		lower := strings.ToLower(t.text)
		if lower == "not" || t.text == "!" {
			p.pos++
			x, err := p.parseNot()
			if err != nil {
				return nil, err
			}
			return notNode{x: x}, nil
		}
	}
	return p.parsePrimary()
}

func (p *parserState) parsePrimary() (node, error) {
	t, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("过滤表达式不完整：运算符后面缺少条件（%s）", supportedHint)
	}
	switch t.text {
	case "(":
		x, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		closing, ok := p.next()
		if !ok || closing.text != ")" {
			return nil, fmt.Errorf("过滤表达式位置 %d：缺少右括号", t.pos+1)
		}
		return x, nil
	case ")":
		return nil, fmt.Errorf("过滤表达式位置 %d：多余的右括号", t.pos+1)
	default:
		return p.parsePrimitive(t)
	}
}

// parsePrimitive 解析一个原子条件（可能带 src/dst 方向前缀）。
func (p *parserState) parsePrimitive(t token) (node, error) {
	lower := strings.ToLower(t.text)
	if name, ok := protoKeywords[lower]; ok {
		return protoNode{name: name}, nil
	}
	switch lower {
	case "src", "dst":
		d := dirSrc
		if lower == "dst" {
			d = dirDst
		}
		sub, ok := p.next()
		if !ok {
			return nil, fmt.Errorf("过滤表达式位置 %d：%q 后面缺少 host/net/port/portrange（%s）", t.pos+1, t.text, supportedHint)
		}
		switch strings.ToLower(sub.text) {
		case "host", "net", "port", "portrange":
			return p.parseQualified(t.pos, d, sub)
		}
		return nil, fmt.Errorf("过滤表达式位置 %d：%q 后面只支持 host/net/port/portrange，不支持 %q（%s）",
			t.pos+1, t.text, sub.text, supportedHint)
	case "host", "net", "port", "portrange":
		return p.parseQualified(t.pos, dirAny, t)
	}
	if unsupportedPrimitives[lower] {
		return nil, fmt.Errorf("过滤表达式位置 %d：暂不支持 pcap 原语 %q（%s）", t.pos+1, t.text, supportedHint)
	}
	return nil, fmt.Errorf("过滤表达式位置 %d：无法识别的写法 %q（%s）", t.pos+1, t.text, supportedHint)
}

// parseQualified 解析带方向前缀的条件本体（host/net/port/portrange）。
func (p *parserState) parseQualified(keyPos int, d dir, kw token) (node, error) {
	arg, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("过滤表达式位置 %d：%q 后面缺少参数（%s）", keyPos+1, kw.text, supportedHint)
	}
	switch strings.ToLower(kw.text) {
	case "host":
		addr, err := parseAddr(arg)
		if err != nil {
			return nil, err
		}
		return hostNode{dir: d, addr: addr}, nil
	case "net":
		pfx, err := parsePrefix(arg)
		if err != nil {
			return nil, err
		}
		return netNode{dir: d, pfx: pfx}, nil
	case "port":
		n, err := parsePort(arg)
		if err != nil {
			return nil, err
		}
		return portNode{dir: d, lo: n, hi: n}, nil
	case "portrange":
		lo, hi, err := parsePortRange(arg)
		if err != nil {
			return nil, err
		}
		return portNode{dir: d, lo: lo, hi: hi}, nil
	}
	return nil, fmt.Errorf("过滤表达式位置 %d：%q 后面不支持 %q（%s）", keyPos+1, kw.text, arg.text, supportedHint)
}

func parseAddr(t token) (netip.Addr, error) {
	addr, err := netip.ParseAddr(t.text)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("过滤表达式位置 %d：%q 不是合法的 IP 地址", t.pos+1, t.text)
	}
	return addr, nil
}

// parsePrefix 解析网段：支持 CIDR 写法，也支持只写地址（按 /32、/128 处理）。
func parsePrefix(t token) (netip.Prefix, error) {
	if pfx, err := netip.ParsePrefix(t.text); err == nil {
		return pfx.Masked(), nil
	}
	addr, err := parseAddr(t)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("过滤表达式位置 %d：%q 不是合法的网段（示例 10.0.0.0/8、2001:db8::/32）", t.pos+1, t.text)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func parsePort(t token) (uint16, error) {
	n, err := strconv.Atoi(t.text)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("过滤表达式位置 %d：%q 不是合法端口（1-65535）", t.pos+1, t.text)
	}
	return uint16(n), nil
}

// parsePortRange 解析 "起-止" 形式的端口区间。
func parsePortRange(t token) (uint16, uint16, error) {
	loText, hiText, ok := strings.Cut(t.text, "-")
	if !ok {
		return 0, 0, fmt.Errorf("过滤表达式位置 %d：portrange 要写成 起-止（示例 1000-2000）", t.pos+1)
	}
	lo, err := parsePort(token{text: loText, pos: t.pos})
	if err != nil {
		return 0, 0, err
	}
	hi, err := parsePort(token{text: hiText, pos: t.pos + len(loText) + 1})
	if err != nil {
		return 0, 0, err
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("过滤表达式位置 %d：端口区间 %q 的起止顺序不对", t.pos+1, t.text)
	}
	return lo, hi, nil
}

// ---------- 匹配 ----------

// matcher 持有当前帧的惰性解析结果：解析器只在真正需要时调用一次。
type matcher struct {
	frame []byte

	eDone bool
	etype uint16
	eOK   bool

	pDone bool
	info  *parser.Info
}

func (m *matcher) etherType() (uint16, bool) {
	if !m.eDone {
		m.eDone = true
		m.etype, m.eOK = parser.EtherType(m.frame)
	}
	return m.etype, m.eOK
}

func (m *matcher) ipInfo() *parser.Info {
	if !m.pDone {
		m.pDone = true
		if info, err := parser.Parse(m.frame); err == nil {
			m.info = info
		}
	}
	return m.info
}

func (n protoNode) match(m *matcher) bool {
	switch n.name {
	case "arp":
		et, ok := m.etherType()
		return ok && et == parser.EthTypeARP
	case "ip":
		info := m.ipInfo()
		return info != nil && !info.IPv6
	case "ip6":
		info := m.ipInfo()
		return info != nil && info.IPv6
	}
	info := m.ipInfo()
	return info != nil && info.Protocol == protoCode(n.name)
}

// protoCode 把协议名映射到 IP 协议号。
func protoCode(name string) uint8 {
	switch name {
	case "tcp":
		return parser.ProtoTCP
	case "udp":
		return parser.ProtoUDP
	case "icmp":
		return parser.ProtoICMP
	case "icmp6":
		return parser.ProtoICMPv6
	}
	return 0
}

func (n hostNode) match(m *matcher) bool {
	info := m.ipInfo()
	if info == nil {
		return false
	}
	if n.addr.Is4() == info.IPv6 { // 地址族不一致：绝不可能是同一个地址
		return false
	}
	switch n.dir {
	case dirSrc:
		return info.SrcIP == n.addr
	case dirDst:
		return info.DstIP == n.addr
	}
	return info.SrcIP == n.addr || info.DstIP == n.addr
}

func (n netNode) match(m *matcher) bool {
	info := m.ipInfo()
	if info == nil {
		return false
	}
	if n.pfx.Addr().Is4() == info.IPv6 {
		return false
	}
	switch n.dir {
	case dirSrc:
		return n.pfx.Contains(info.SrcIP)
	case dirDst:
		return n.pfx.Contains(info.DstIP)
	}
	return n.pfx.Contains(info.SrcIP) || n.pfx.Contains(info.DstIP)
}

func (n portNode) match(m *matcher) bool {
	info := m.ipInfo()
	if info == nil {
		return false
	}
	// ICMP 等协议没有端口：端口条件只对 TCP/UDP 成立（与 pcap 一致）。
	if info.Protocol != parser.ProtoTCP && info.Protocol != parser.ProtoUDP {
		return false
	}
	hit := func(p uint16) bool { return p >= n.lo && p <= n.hi }
	switch n.dir {
	case dirSrc:
		return hit(info.SrcPort)
	case dirDst:
		return hit(info.DstPort)
	}
	return hit(info.SrcPort) || hit(info.DstPort)
}

func (n notNode) match(m *matcher) bool { return !n.x.match(m) }
func (n andNode) match(m *matcher) bool { return n.l.match(m) && n.r.match(m) }
func (n orNode) match(m *matcher) bool  { return n.l.match(m) || n.r.match(m) }

// ---------- 文本输出 ----------

func (n protoNode) String() string { return n.name }

func (n hostNode) String() string { return n.dir.prefix() + "host " + n.addr.String() }

func (n netNode) String() string { return n.dir.prefix() + "net " + n.pfx.String() }

func (n portNode) String() string {
	if n.lo == n.hi {
		return n.dir.prefix() + "port " + strconv.Itoa(int(n.lo))
	}
	return n.dir.prefix() + "portrange " + strconv.Itoa(int(n.lo)) + "-" + strconv.Itoa(int(n.hi))
}

func (n notNode) String() string { return "not " + wrap(n.x, compound(n.x)) }

func (n andNode) String() string {
	return wrap(n.l, isOrNode(n.l)) + " and " + wrap(n.r, isOrNode(n.r))
}

func (n orNode) String() string {
	return wrap(n.l, isAndNode(n.l)) + " or " + wrap(n.r, isAndNode(n.r))
}

func wrap(n node, paren bool) string {
	if paren {
		return "(" + n.String() + ")"
	}
	return n.String()
}

func compound(n node) bool { return isAndNode(n) || isOrNode(n) }

func isAndNode(n node) bool { _, ok := n.(andNode); return ok }
func isOrNode(n node) bool  { _, ok := n.(orNode); return ok }
