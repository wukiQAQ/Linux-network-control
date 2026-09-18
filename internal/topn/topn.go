// Package topn 把会话记录聚合成排行榜：TOP IP、协议分布、TOP 端口。
// 纯逻辑、无外部依赖，便于单元测试；API 层只负责取数与返回。
package topn

import (
	"sort"
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

type counter struct {
	flows   int
	packets uint64
	bytes   uint64
}

func add(m map[string]*counter, key string, r storage.FlowRecord) {
	if key == "" || key == "0" {
		return
	}
	c := m[key]
	if c == nil {
		c = &counter{}
		m[key] = c
	}
	c.flows++
	c.packets += r.Packets
	c.bytes += r.Bytes
}

// finalize 排序、截断并计算占比。
func finalize(m map[string]*counter, n int) []Entry {
	var total uint64
	for _, c := range m {
		total += c.bytes
	}
	list := make([]Entry, 0, len(m))
	for k, c := range m {
		e := Entry{Key: k, Flows: c.flows, Packets: c.packets, Bytes: c.bytes}
		if total > 0 {
			e.Percent = float64(int64(c.bytes)*1000/int64(total)) / 10
		}
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Bytes != list[j].Bytes {
			return list[i].Bytes > list[j].Bytes
		}
		return list[i].Key < list[j].Key
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

// Aggregate 聚合会话记录：IP 与端口按双向计入（一个会话的两端都会上榜），协议按整条会话计入。
func Aggregate(records []storage.FlowRecord, n int) Result {
	if n <= 0 {
		n = 10
	}
	ip := map[string]*counter{}
	proto := map[string]*counter{}
	port := map[string]*counter{}
	res := Result{TotalFlows: len(records)}
	for _, r := range records {
		res.TotalBytes += r.Bytes
		add(ip, r.SrcIP, r)
		add(ip, r.DstIP, r)
		add(proto, ProtoName(r.Proto), r)
		if r.Proto == 6 || r.Proto == 17 {
			if r.SrcPort != 0 {
				add(port, strconv.Itoa(int(r.SrcPort)), r)
			}
			if r.DstPort != 0 {
				add(port, strconv.Itoa(int(r.DstPort)), r)
			}
		}
	}
	res.ByIP = finalize(ip, n)
	res.ByProto = finalize(proto, n)
	res.ByPort = finalize(port, n)
	return res
}
