// bpf.go —— 把过滤表达式编译成"内核可用的保守超集程序"（Linux SO_ATTACH_FILTER）。
//
// 设计要点：
//  1. 内核程序只做**剪枝**，精确判定始终由用户态 Match 完成；
//  2. 因此程序必须满足不变式：**用户态会命中的帧，内核程序必须放行**（宁可多放行，绝不误丢）。
//     凡是无法在固定偏移上安全判断的情况（VLAN 标签、IPv6 扩展头、IP 分片、not 子表达式）一律保守放行；
//  3. 程序开头先做长度检查：帧长不足时直接放行 —— 内核遇到越界 load 会整包丢弃，会破坏上面的不变式；
//  4. 指令集只用经典 BPF 的极小子集：ld [k]（W/H/B）、ld #len、and #k、jeq/ja、ret #k。
//     叶子片段内部的跳转是 8 位偏移（片段很短），片段之间用 32 位 JA，因此不会跳转越界。
package filter

import (
	"encoding/binary"
	"net/netip"

	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// Inst 是一条经典 BPF 指令，与内核 struct sock_filter 一一对应。
type Inst struct {
	Code uint16
	JT   uint8
	JF   uint8
	K    uint32
}

// Program 是一段经典 BPF 程序（Linux 上直接交给 SO_ATTACH_FILTER）。
type Program []Inst

const (
	// snapLen 是放行时返回给内核的"保留字节数"：256KB 远大于单帧上限（读缓冲 64KB），不会截断。
	snapLen = 0x40000
	// maxProgram 是 SO_ATTACH_FILTER 的指令条数上限（内核限制 4096 条）。
	maxProgram = 4096
	// lenPrefix 是长度检查前缀占用的指令数。
	lenPrefix = 3

	// 指令编码（Linux <linux/filter.h> 的经典 BPF 子集）
	opLdW   = 0x20 // BPF_LD | BPF_W | BPF_ABS
	opLdH   = 0x28 // BPF_LD | BPF_H | BPF_ABS
	opLdB   = 0x30 // BPF_LD | BPF_B | BPF_ABS
	opLdLen = 0x80 // BPF_LD | BPF_LEN
	opAndK  = 0x54 // BPF_ALU | BPF_AND | BPF_K
	opJa    = 0x05 // BPF_JMP | BPF_JA
	opJeqK  = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	opJgeK  = 0x35 // BPF_JMP | BPF_JGE | BPF_K
	opRetK  = 0x06 // BPF_RET | BPF_K
)

func retInst(k uint32) Inst { return Inst{Code: opRetK, K: k} }

// frag 是编译中间结果：一段自包含的指令序列，
// accepts / rejects 记录"命中 / 未命中终结指令"的下标（组合时会被改写成跳转）。
type frag struct {
	insts   []Inst
	accepts []int
	rejects []int
	maxRead int // 该片段读取过的最大字节偏移（用于长度检查前缀）
}

// acceptAll 表示"没有任何剪枝价值"（内核全部放行）。
var acceptAll = frag{insts: []Inst{retInst(snapLen)}, accepts: []int{0}}

func isAcceptAll(f frag) bool {
	return len(f.insts) == 1 && f.insts[0].Code == opRetK && f.insts[0].K == snapLen
}

// body 用于拼装叶子片段，并记录需要回填的跳转位置。
type body struct {
	insts     []Inst
	expects   []int // 条件不满足 → 跳 reject（8 位偏移）
	accepts   []int // 条件满足 → 跳 accept（8 位偏移）
	failJumps []int // 无条件跳 reject（32 位偏移）
	maxRead   int
}

func (b *body) emit(i Inst) int {
	b.insts = append(b.insts, i)
	return len(b.insts) - 1
}

func (b *body) countRead(off, width int) {
	if off+width > b.maxRead {
		b.maxRead = off + width
	}
}

func (b *body) ldw(off int) { b.countRead(off, 4); b.emit(Inst{Code: opLdW, K: uint32(off)}) }
func (b *body) ldh(off int) { b.countRead(off, 2); b.emit(Inst{Code: opLdH, K: uint32(off)}) }
func (b *body) ldb(off int) { b.countRead(off, 1); b.emit(Inst{Code: opLdB, K: uint32(off)}) }

// expectEq：相等则继续，否则本片段失败（跳 reject）。
func (b *body) expectEq(k uint32) { b.expects = append(b.expects, b.emit(Inst{Code: opJeqK, K: k})) }

// acceptEq：相等则直接放行（保守分支），否则继续。
func (b *body) acceptEq(k uint32) { b.accepts = append(b.accepts, b.emit(Inst{Code: opJeqK, K: k})) }

// failNow：无条件跳 reject（用于"或"型叶子收尾）。
func (b *body) failNow() { b.failJumps = append(b.failJumps, b.emit(Inst{Code: opJa})) }

// leaf 收尾：追加三个终结指令并回填跳转，片段过长时返回 false（调用方退化为不剪枝）。
//
//   - aFrag ：片段命中（组合时会被改写成"跳到另一侧"，所以必须放在 fall-through 位置）
//   - aFinal：**保守放行**终点（VLAN / IPv6 等无法精确判断的分支走这里，永远不参与组合改写）
//   - r     ：未命中
//
// 区分这两个放行终点很关键：组合"与"时片段命中要变成"继续判断另一侧"，
// 而保守放行必须仍然是最终放行，否则 VLAN 帧会被后面的地址判断误杀。
func (b *body) leaf() (frag, bool) {
	aFrag := b.emit(retInst(snapLen))
	aFinal := b.emit(retInst(snapLen))
	r := b.emit(retInst(0))
	for _, pos := range b.expects {
		d := r - pos - 1
		if d < 0 || d > 255 {
			return frag{}, false
		}
		b.insts[pos].JF = uint8(d)
	}
	for _, pos := range b.accepts {
		d := aFinal - pos - 1
		if d < 0 || d > 255 {
			return frag{}, false
		}
		b.insts[pos].JT = uint8(d)
	}
	for _, pos := range b.failJumps {
		b.insts[pos].K = uint32(r - pos - 1)
	}
	return frag{insts: b.insts, accepts: []int{aFrag}, rejects: []int{r}, maxRead: b.maxRead}, true
}

// fragAnd 组合成"与"：左片命中 → 跳到右片入口，右片结果即最终结果。
func fragAnd(l, r frag) frag {
	shift := len(l.insts)
	insts := make([]Inst, 0, shift+len(r.insts))
	insts = append(insts, l.insts...)
	insts = append(insts, r.insts...)
	for _, pos := range l.accepts {
		insts[pos] = Inst{Code: opJa, K: uint32(shift - pos - 1)}
	}
	out := frag{insts: insts, maxRead: max(l.maxRead, r.maxRead)}
	out.accepts = shiftIndexes(r.accepts, shift)
	out.rejects = append(shiftIndexes(l.rejects, 0), shiftIndexes(r.rejects, shift)...)
	return out
}

// fragOr 组合成"或"：左片未命中 → 跳到右片入口，左片命中即放行。
func fragOr(l, r frag) frag {
	shift := len(l.insts)
	insts := make([]Inst, 0, shift+len(r.insts))
	insts = append(insts, l.insts...)
	insts = append(insts, r.insts...)
	for _, pos := range l.rejects {
		insts[pos] = Inst{Code: opJa, K: uint32(shift - pos - 1)}
	}
	out := frag{insts: insts, maxRead: max(l.maxRead, r.maxRead)}
	out.accepts = append(shiftIndexes(l.accepts, 0), shiftIndexes(r.accepts, shift)...)
	out.rejects = shiftIndexes(r.rejects, shift)
	return out
}

func shiftIndexes(idx []int, shift int) []int {
	out := make([]int, len(idx))
	for i, v := range idx {
		out[i] = v + shift
	}
	return out
}

// linkGuard 负责链路层"安全网"：VLAN 标签（内核不解析）一律保守放行，
// 之后再按 EtherType 判断。返回片段表示"链路层检查通过"。
func linkGuard(b *body, wantEther uint16) {
	b.ldh(12)
	b.acceptEq(uint32(parser.EthTypeVLAN))
	b.acceptEq(uint32(parser.EthTypeQinQ))
	b.expectEq(uint32(wantEther))
}

// compileProto 生成协议类条件的超集程序。
func compileProto(name string) (frag, bool) {
	b := &body{}
	switch name {
	case "arp":
		linkGuard(b, parser.EthTypeARP)
		return b.leaf()
	case "ip":
		linkGuard(b, parser.EthTypeIPv4)
		return b.leaf()
	case "ip6":
		linkGuard(b, parser.EthTypeIPv6)
		return b.leaf()
	}
	// tcp / udp / icmp / icmp6：
	//   - IPv4：协议号在固定偏移（23），可以精确剪枝；
	//   - IPv6：扩展头会改变协议字节，而内核程序不解析扩展头 → 保守放行；
	//   - 其它链路类型：用户态也不会命中，直接拒绝。
	b.ldh(12)
	b.acceptEq(uint32(parser.EthTypeVLAN))
	b.acceptEq(uint32(parser.EthTypeQinQ))
	b.acceptEq(uint32(parser.EthTypeIPv6))
	b.expectEq(uint32(parser.EthTypeIPv4))
	b.ldb(23)
	b.expectEq(uint32(protoCode(name)))
	return b.leaf()
}

// compilePort 生成端口类条件的超集程序：端口只可能出现在 TCP/UDP 里。
func compilePort() (frag, bool) {
	b := &body{}
	b.ldh(12)
	b.acceptEq(uint32(parser.EthTypeVLAN))
	b.acceptEq(uint32(parser.EthTypeQinQ))
	b.acceptEq(uint32(parser.EthTypeIPv6)) // 同协议类：IPv6 侧保守放行
	b.expectEq(uint32(parser.EthTypeIPv4))
	b.ldb(23)
	b.acceptEq(uint32(parser.ProtoTCP))
	b.acceptEq(uint32(parser.ProtoUDP))
	b.failNow()
	return b.leaf()
}

// maskForBits 返回 32 位掩码中前 bits 位为 1 的值。
func maskForBits(bits int) uint32 {
	switch {
	case bits <= 0:
		return 0
	case bits >= 32:
		return 0xffffffff
	default:
		return ^uint32(0) << (32 - bits)
	}
}

// addrWords 把地址按前缀长度拆成 32 位字与掩码（IPv4 一个词、IPv6 四个词）。
func addrWords(addr netip.Addr, bits int) (words, masks []uint32) {
	if addr.Is4() {
		a := addr.As4()
		m := maskForBits(bits)
		return []uint32{binary.BigEndian.Uint32(a[:]) & m}, []uint32{m}
	}
	a := addr.As16()
	words = make([]uint32, 4)
	masks = make([]uint32, 4)
	for i := 0; i < 4; i++ {
		m := maskForBits(bits - i*32)
		masks[i] = m
		words[i] = binary.BigEndian.Uint32(a[i*4:i*4+4]) & m
	}
	return words, masks
}

// candidate 生成"某个地址位置与给定前缀相等"的片段。
func candidate(off int, words, masks []uint32) (frag, bool) {
	b := &body{}
	for i := range words {
		b.ldw(off + 4*i)
		if masks[i] != 0xffffffff {
			b.emit(Inst{Code: opAndK, K: masks[i]})
		}
		b.expectEq(words[i])
	}
	return b.leaf()
}

// addrFrag 按方向组合源 / 目的地址判断。
func addrFrag(d dir, srcOff, dstOff int, srcW, srcM, dstW, dstM []uint32) (frag, bool) {
	switch d {
	case dirSrc:
		return candidate(srcOff, srcW, srcM)
	case dirDst:
		return candidate(dstOff, dstW, dstM)
	}
	a, ok := candidate(srcOff, srcW, srcM)
	if !ok {
		return frag{}, false
	}
	b, ok := candidate(dstOff, dstW, dstM)
	if !ok {
		return frag{}, false
	}
	return fragOr(a, b), true
}

// compileAddr 生成主机 / 网段条件的超集程序。
func compileAddr(d dir, addr netip.Addr, bits int) (frag, bool) {
	b := &body{}
	is4 := addr.Is4()
	if is4 {
		linkGuard(b, parser.EthTypeIPv4)
	} else {
		linkGuard(b, parser.EthTypeIPv6)
	}
	guard, ok := b.leaf()
	if !ok {
		return acceptAll, true
	}
	srcOff, dstOff := 26, 30
	if !is4 {
		srcOff, dstOff = 22, 38
	}
	words, masks := addrWords(addr, bits)
	cand, ok := addrFrag(d, srcOff, dstOff, words, masks, words, masks)
	if !ok {
		return acceptAll, true
	}
	return fragAnd(guard, cand), true
}

// compileSuperset 递归编译：返回的一定是"原条件的超集"。
func compileSuperset(n node) frag {
	switch t := n.(type) {
	case protoNode:
		if f, ok := compileProto(t.name); ok {
			return f
		}
	case hostNode:
		if f, ok := compileAddr(t.dir, t.addr, t.addr.BitLen()); ok {
			return f
		}
	case netNode:
		if f, ok := compileAddr(t.dir, t.pfx.Addr(), t.pfx.Bits()); ok {
			return f
		}
	case portNode:
		if f, ok := compilePort(); ok {
			return f
		}
	case andNode:
		l, r := compileSuperset(t.l), compileSuperset(t.r)
		if isAcceptAll(l) {
			return r
		}
		if isAcceptAll(r) {
			return l
		}
		return fragAnd(l, r)
	case orNode:
		l, r := compileSuperset(t.l), compileSuperset(t.r)
		if isAcceptAll(l) || isAcceptAll(r) {
			return acceptAll
		}
		return fragOr(l, r)
	}
	// not 子表达式（以及任何无法表达的写法）：只能全部放行
	return acceptAll
}

// KernelProgram 返回可下沉到内核的保守 BPF 程序。
// 第二个返回值为 false 表示"没有剪枝价值"（无法表达，或退化为全部放行），
// 调用方应继续只用用户态过滤。
func (f *Filter) KernelProgram() (Program, bool) {
	if f == nil {
		return nil, false
	}
	fr := compileSuperset(f.root)
	if isAcceptAll(fr) || len(fr.insts) == 0 {
		return nil, false
	}
	prog := Program(fr.insts)
	if fr.maxRead > 0 {
		prog = prependLenCheck(prog, fr.maxRead, fr.accepts[0])
	}
	if len(prog) > maxProgram {
		return nil, false
	}
	return prog, true
}

// prependLenCheck 在程序前插入"帧长不足则放行"的前缀：
// 内核遇到越界 load 会整包丢弃，而用户态可能仍然接受该帧（例如表达式里有 or / not），
// 所以这里必须在长度不足时直接跳到放行分支，才能保持"内核程序是用户态条件的超集"这一不变式。
func prependLenCheck(prog Program, need, acceptIdx int) Program {
	out := make(Program, 0, len(prog)+lenPrefix)
	out = append(out, Inst{Code: opLdLen})
	out = append(out, Inst{Code: opJgeK, JT: 1, JF: 0, K: uint32(need)})
	out = append(out, Inst{Code: opJa})
	out = append(out, prog...)
	target := acceptIdx + lenPrefix
	out[lenPrefix-1].K = uint32(target - (lenPrefix - 1) - 1)
	return out
}
