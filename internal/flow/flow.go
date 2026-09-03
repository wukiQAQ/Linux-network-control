// Package flow 实现会话（5 元组）聚合与流表管理。
// 核心思想：A→B 与 B→A 的报文在表内归并为同一条流（Key 双向归一化），
// 记录包数/字节/起止时间，并通过"空闲 + 硬超时"双策略过期释放内存。
package flow

import (
	"net/netip"
	"sync"
	"time"
)

// Key 是 5 元组；Canonical() 返回与方向无关的规范形式，用于双向归并。
type Key struct {
	Src, Dst     netip.Addr
	SPort, DPort uint16
	Proto        uint8
}

// Canonical 返回排序后的 key：两个方向的 key 得到同一个规范值。
func (k Key) Canonical() Key {
	inv := Key{Src: k.Dst, Dst: k.Src, SPort: k.DPort, DPort: k.SPort, Proto: k.Proto}
	if cmpKey(k, inv) <= 0 {
		return k
	}
	return inv
}

// Flow 是一条会话的统计状态。
// Src/Dst 记录"最先观测到"的连线方向，便于界面按发起方展示。
type Flow struct {
	Key      Key
	Start    time.Time
	End      time.Time
	Packets  uint64
	Bytes    uint64
	TCPFlags uint8
	SrcIP    netip.Addr // 首次观测方向
	DstIP    netip.Addr
	SPort    uint16
	DPort    uint16
}

// Table 是并发安全的会话表。
type Table struct {
	mu    sync.Mutex
	m     map[Key]*Flow
	idle  time.Duration // 空闲超时
	hard  time.Duration // 硬超时
	clock func() time.Time

	created uint64 // 历史创建总数，供聚合层计算"新建连接速率"
}

// NewTable 创建流表。idle/hard 为过期阈值；clock 便于测试注入时间。
func NewTable(idle, hard time.Duration, clock func() time.Time) *Table {
	if clock == nil {
		clock = time.Now
	}
	return &Table{m: make(map[Key]*Flow), idle: idle, hard: hard, clock: clock}
}

// Handle 处理一个解析后的报文：命中则更新，未命中则新建。
func (t *Table) Handle(proto uint8, src, dst netip.Addr, sport, dport uint16, flags uint8, length int, ts time.Time) {
	k := Key{Src: src, Dst: dst, SPort: sport, DPort: dport, Proto: proto}.Canonical()
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.m[k]
	if f == nil {
		f = &Flow{
			Key: k, Start: ts, End: ts,
			SrcIP: src, DstIP: dst, SPort: sport, DPort: dport,
		}
		t.m[k] = f
		t.created++
	}
	f.End = ts
	f.Packets++
	f.Bytes += uint64(length)
	f.TCPFlags |= flags
}

// Expire 清理超时会话并返回其快照（供存储层落盘）。
func (t *Table) Expire(now time.Time) []Flow {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Flow
	for k, f := range t.m {
		if now.Sub(f.Start) >= t.hard || now.Sub(f.End) >= t.idle {
			out = append(out, *f)
			delete(t.m, k)
		}
	}
	return out
}

// Active 返回当前活跃会话数（并发连接数指标来源）。
func (t *Table) Active() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

// Created 返回历史上累计创建的会话数。
func (t *Table) Created() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.created
}

// Snapshot 返回当前全部会话（健康页/调试用）。
func (t *Table) Snapshot() []Flow {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Flow, 0, len(t.m))
	for _, f := range t.m {
		out = append(out, *f)
	}
	return out
}

// cmpKey 全字段字典序比较，保证双向 key 归一化后一致。
func cmpKey(a, b Key) int {
	if c := a.Src.Compare(b.Src); c != 0 {
		return c
	}
	if c := cmp16(a.SPort, b.SPort); c != 0 {
		return c
	}
	if a.Proto != b.Proto {
		if a.Proto < b.Proto {
			return -1
		}
		return 1
	}
	if c := a.Dst.Compare(b.Dst); c != 0 {
		return c
	}
	return cmp16(a.DPort, b.DPort)
}

func cmp16(a, b uint16) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
