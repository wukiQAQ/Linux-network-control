// Package capture 定义采集层统一接口，并提供多种数据源实现：
//   - Synthetic：程序内构造报文（演示/开发）
//   - PcapFile ：回放 pcap 文件（离线测试，全功能可用）
//   - Live     ：Linux 原生 AF_PACKET 抓包（build tag: linux）
//
// 上层（解析、流表）只依赖 Source 接口，新增数据源不影响业务逻辑。
package capture

import (
	"context"
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

// atomicStats 为数据源提供并发安全的计数。
type atomicStats struct {
	packets atomic.Uint64
	drops   atomic.Uint64
}

func (s *atomicStats) addPacket() { s.packets.Add(1) }
func (s *atomicStats) addDrop()   { s.drops.Add(1) }
func (s *atomicStats) snapshot() Stats {
	return Stats{Packets: s.packets.Load(), Drops: s.drops.Load()}
}
