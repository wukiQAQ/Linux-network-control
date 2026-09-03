// Package aggregator 把逐包计数压缩为秒级指标（bps/pps/连接数/丢包）。
// 设计原则：只做"增量累加 + 定时快照"，不做全量重算。
package aggregator

import (
	"sync"
	"time"
)

// Sample 是一个聚合周期（默认 1s）的指标快照。
type Sample struct {
	Ts         time.Time
	Bytes      uint64
	Packets    uint64
	Bps        float64
	Pps        float64
	Active     int
	NewConns   int
	DropEvents int
}

// Agg 把逐包事件累加并按 tick 输出采样。
type Agg struct {
	mu      sync.Mutex
	bytes   uint64
	pkts    uint64 // 当前周期包数（tick 后清零）
	total   uint64 // 历史累计包数（健康页收包总数）
	drops   uint64
	lastSec uint64
	lastD   uint64
	last    time.Time
	ring    []Sample
	maxRing int
}

func New(maxSamples int) *Agg {
	if maxSamples <= 0 {
		maxSamples = 3600
	}
	return &Agg{maxRing: maxSamples}
}

func (a *Agg) Observe(length int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bytes += uint64(length)
	a.pkts++
	a.total++
}

func (a *Agg) RecordDrop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.drops++
}

func (a *Agg) TotalPackets() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total
}

func (a *Agg) TotalDrops() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.drops
}

// Tick 输出采样并清零周期计数。active/created 由流表提供。
func (a *Agg) Tick(now time.Time, active int, created uint64) Sample {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Sample{Ts: now}
	if a.last.IsZero() {
		a.last = now // 首帧建立基线，启动期间的计数按 1s 折算
	}
	sec := now.Sub(a.last).Seconds()
	if sec <= 0 {
		sec = 1
	}
	s.Bytes, s.Packets = a.bytes, a.pkts
	s.Bps = float64(a.bytes) * 8 / sec
	s.Pps = float64(a.pkts) / sec
	s.DropEvents = int(a.drops - a.lastD)
	s.Active = active
	if created > a.lastSec {
		s.NewConns = int(created - a.lastSec)
	}
	s.Active = active
	if created > a.lastSec {
		s.NewConns = int(created - a.lastSec)
	}
	a.bytes, a.pkts = 0, 0
	a.lastSec = created
	a.lastD = a.drops
	a.last = now
	a.ring = append(a.ring, s)
	if len(a.ring) > a.maxRing {
		a.ring = a.ring[len(a.ring)-a.maxRing:]
	}
	return s
}

func (a *Agg) Last() Sample {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.ring) == 0 {
		return Sample{}
	}
	return a.ring[len(a.ring)-1]
}

func (a *Agg) History() []Sample {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Sample, len(a.ring))
	copy(out, a.ring)
	return out
}
