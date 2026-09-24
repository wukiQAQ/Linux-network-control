// Package capture 定义采集层统一接口，并提供多种数据源实现：
//   - Synthetic：程序内构造报文（演示/开发）
//   - PcapFile ：回放 pcap 文件（离线测试，全功能可用）
//   - Live     ：Linux 原生 AF_PACKET 抓包（build tag: linux）
//
// 上层（解析、流表）只依赖 Source 接口，新增数据源不影响业务逻辑。
package capture

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Packet 是采集层输出的原始报文。
type Packet struct {
	Ts    time.Time
	Raw   []byte
	Iface string
}

// Stats 暴露数据源自身的健康指标（收包/丢包），这是"监控自身可观测"的基础。
type Stats struct {
	Packets uint64
	Drops   uint64
}

// Source 是所有数据源必须实现的接口。
type Source interface {
	Next(ctx context.Context) (*Packet, error) // 阻塞返回下一个报文；ctx 取消时返回 ctx.Err
	Close() error
	Stats() Stats
}

// 多队列并行收包的并发度范围（1 = 单队列，保持默认行为）。
const (
	MinReaders = 1
	MaxReaders = 8
)

// normalizeReaders 把配置里的并发度夹到 [1,8]，非法值回退 1。
func normalizeReaders(n int) int {
	if n < MinReaders {
		return MinReaders
	}
	if n > MaxReaders {
		return MaxReaders
	}
	return n
}

// statsPollEvery 控制"多久查询一次内核侧统计"（每 N 个包一次，避免每包一次系统调用）。
const statsPollEvery = 128

// shouldPollStats 判断本次读包后是否需要刷新内核统计。
func shouldPollStats(n uint64) bool { return n > 0 && n%statsPollEvery == 0 }

// atomicStats 为数据源提供并发安全的计数。
type atomicStats struct {
	packets atomic.Uint64
	drops   atomic.Uint64
}

// planCPUAssignment 给 n 个读循环分配 CPU：优先一个循环一个核，核不够时按顺序循环复用；
// allowed 为空时返回 n 个 -1，表示不绑定（保持原有调度行为）。
func planCPUAssignment(allowed []int, n int) []int {
	out := make([]int, n)
	if n <= 0 {
		return out
	}
	valid := make([]int, 0, len(allowed))
	for _, c := range allowed {
		if c >= 0 {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		for i := range out {
			out[i] = -1
		}
		return out
	}
	for i := range out {
		out[i] = valid[i%len(valid)]
	}
	return out
}

// cpuPlanText 把 CPU 分配结果整理成一行日志文本。
func cpuPlanText(plan []int) string {
	parts := make([]string, 0, len(plan))
	for i, cpu := range plan {
		if cpu < 0 {
			parts = append(parts, fmt.Sprintf("reader%d→不绑定", i))
			continue
		}
		parts = append(parts, fmt.Sprintf("reader%d→CPU%d", i, cpu))
	}
	return strings.Join(parts, " ")
}

func (s *atomicStats) addPacket() { s.packets.Add(1) }
func (s *atomicStats) addDrop()   { s.drops.Add(1) }
func (s *atomicStats) snapshot() Stats {
	return Stats{Packets: s.packets.Load(), Drops: s.drops.Load()}
}
