package topn

import (
	"testing"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

func TestProtoName(t *testing.T) {
	cases := map[uint8]string{6: "TCP", 17: "UDP", 1: "ICMP", 58: "ICMPv6", 47: "47"}
	for proto, want := range cases {
		if got := ProtoName(proto); got != want {
			t.Errorf("ProtoName(%d) = %q, want %q", proto, got, want)
		}
	}
}

// TestFromAggregate 校验展示层映射：协议号 key 变成可读名称，其余维度与占比原样传递。
func TestFromAggregate(t *testing.T) {
	agg := storage.FlowAggregate{
		TotalFlows: 3,
		TotalBytes: 1700,
		ByIP: []storage.AggEntry{
			{Key: "8.8.8.8", Flows: 2, Packets: 15, Bytes: 1500, Percent: 44.1},
			{Key: "10.0.0.1", Flows: 2, Packets: 12, Bytes: 1200, Percent: 35.3},
		},
		ByProto: []storage.AggEntry{
			{Key: "6", Flows: 2, Bytes: 1500, Percent: 88.2},
			{Key: "17", Flows: 1, Bytes: 200, Percent: 11.8},
			{Key: "47", Flows: 1, Bytes: 10, Percent: 0.6},
		},
		ByPort: []storage.AggEntry{{Key: "443", Flows: 2, Bytes: 1500, Percent: 75}},
	}
	res := FromAggregate(agg)
	if res.TotalFlows != 3 || res.TotalBytes != 1700 {
		t.Fatalf("总量传递错误: %+v", res)
	}
	if res.ByIP[0].Key != "8.8.8.8" || res.ByIP[0].Percent != 44.1 {
		t.Errorf("IP 维度应原样传递: %+v", res.ByIP[0])
	}
	if res.ByProto[0].Key != "TCP" || res.ByProto[1].Key != "UDP" || res.ByProto[2].Key != "47" {
		t.Errorf("协议名映射错误: %+v", res.ByProto)
	}
	if res.ByPort[0].Key != "443" {
		t.Errorf("端口维度应原样传递: %+v", res.ByPort)
	}
}

// TestFromAggregateEmpty 校验空输入返回空切片（JSON 里是 [] 而不是 null，前端不必额外判空）。
func TestFromAggregateEmpty(t *testing.T) {
	res := FromAggregate(storage.FlowAggregate{})
	if res.ByIP == nil || res.ByProto == nil || res.ByPort == nil {
		t.Fatalf("应返回空切片而不是 nil: %+v", res)
	}
	if len(res.ByIP) != 0 || res.TotalFlows != 0 {
		t.Errorf("空输入应得到空结果: %+v", res)
	}
}
