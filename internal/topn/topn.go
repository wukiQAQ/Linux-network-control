// Package topn 把存储层的会话聚合结果整理成排行榜：TOP IP、协议分布、TOP 端口。
//
// 聚合本身在存储层完成（SQLite 走 SQL 聚合，不再受"只取前 N 条样本"限制），
// 这里只做展示层映射：协议号 → 可读名称，以及 JSON 结构定义。
package topn

import (
	"strconv"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// Entry 是排行中的一项。
type Entry struct {
	Key     string  `json:"key"`
	Flows   int     `json:"flows"`
	Packets uint64  `json:"packets"`
	Bytes   uint64  `json:"bytes"`
	Percent float64 `json:"percent"` // 在该维度总量中的字节占比（0~100）
}

// Result 是一次聚合的结果。
type Result struct {
	TotalFlows int     `json:"total_flows"`
	TotalBytes uint64  `json:"total_bytes"`
	ByIP       []Entry `json:"by_ip"`
	ByProto    []Entry `json:"by_proto"`
	ByPort     []Entry `json:"by_port"`
}

// ProtoName 把协议号转成可读名称。
func ProtoName(p uint8) string {
	switch p {
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 1:
		return "ICMP"
	case 58:
		return "ICMPv6"
	default:
		return strconv.Itoa(int(p))
	}
}

// FromAggregate 把存储层的聚合结果转成展示结构（协议维度把 "6" 映射成 "TCP"）。
func FromAggregate(agg storage.FlowAggregate) Result {
	return Result{
		TotalFlows: agg.TotalFlows,
		TotalBytes: agg.TotalBytes,
		ByIP:       convert(agg.ByIP, nil),
		ByProto:    convert(agg.ByProto, protoKeyName),
		ByPort:     convert(agg.ByPort, nil),
	}
}

func convert(entries []storage.AggEntry, rename func(string) string) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		key := e.Key
		if rename != nil {
			key = rename(key)
		}
		out = append(out, Entry{Key: key, Flows: e.Flows, Packets: e.Packets, Bytes: e.Bytes, Percent: e.Percent})
	}
	return out
}

// protoKeyName 把存储层的协议 key（"6"）转成展示名（"TCP"）；解析失败时原样返回。
func protoKeyName(key string) string {
	n, err := strconv.Atoi(key)
	if err != nil {
		return key
	}
	return ProtoName(uint8(n))
}
