package topn

import (
	"testing"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

func TestAggregate(t *testing.T) {
	recs := []storage.FlowRecord{
		{SrcIP: "10.0.0.1", DstIP: "8.8.8.8", SrcPort: 40000, DstPort: 443, Proto: 6, Packets: 10, Bytes: 1000},
		{SrcIP: "10.0.0.1", DstIP: "1.1.1.1", SrcPort: 40001, DstPort: 53, Proto: 17, Packets: 2, Bytes: 200},
		{SrcIP: "10.0.0.2", DstIP: "8.8.8.8", SrcPort: 40002, DstPort: 443, Proto: 6, Packets: 5, Bytes: 500},
	}
	res := Aggregate(recs, 2)
	if res.TotalFlows != 3 || res.TotalBytes != 1700 {
		t.Fatalf("总量统计错误: %+v", res)
	}
	// IP 双向计入：8.8.8.8 出现在两条会话（1000+500=1500）排第一，10.0.0.1（1000+200=1200）第二
	if len(res.ByIP) != 2 {
		t.Fatalf("TOP IP 数量错误: %+v", res.ByIP)
	}
	if res.ByIP[0].Key != "8.8.8.8" || res.ByIP[0].Bytes != 1500 {
		t.Fatalf("TOP IP 第一名错误: %+v", res.ByIP)
	}
	if res.ByIP[1].Key != "10.0.0.1" || res.ByIP[1].Bytes != 1200 {
		t.Fatalf("TOP IP 第二名错误: %+v", res.ByIP)
	}
	if res.ByIP[0].Percent < 43 || res.ByIP[0].Percent > 45 {
		t.Errorf("占比应在 44%% 左右，实际 %v", res.ByIP[0].Percent)
	}
	// 协议：TCP 1500 字节排第一，UDP 次之
	if len(res.ByProto) != 2 || res.ByProto[0].Key != "TCP" || res.ByProto[1].Key != "UDP" {
		t.Fatalf("协议分布错误: %+v", res.ByProto)
	}
	if res.ByProto[0].Bytes != 1500 {
		t.Errorf("TCP 字节数应为 1500，实际 %d", res.ByProto[0].Bytes)
	}
	// 端口：443 出现两次（1000+500）排第一
	if len(res.ByPort) != 2 || res.ByPort[0].Key != "443" || res.ByPort[0].Bytes != 1500 {
		t.Fatalf("TOP 端口错误: %+v", res.ByPort)
	}
}

func TestAggregateEdgeCases(t *testing.T) {
	if res := Aggregate(nil, 0); res.TotalFlows != 0 || len(res.ByIP) != 0 {
		t.Errorf("空输入应返回空结果: %+v", res)
	}
	// ICMP 无端口，不应出现在端口排行里
	res := Aggregate([]storage.FlowRecord{{SrcIP: "10.0.0.9", DstIP: "10.0.0.10", Proto: 1, Packets: 1, Bytes: 64}}, 5)
	if len(res.ByPort) != 0 {
		t.Errorf("ICMP 不应进入端口排行: %+v", res.ByPort)
	}
	if res.ByProto[0].Key != "ICMP" {
		t.Errorf("协议名错误: %+v", res.ByProto)
	}
	// IPv6 地址参与排行
	res = Aggregate([]storage.FlowRecord{{SrcIP: "2001:db8::1", DstIP: "2400:3200::1", SrcPort: 1234, DstPort: 80, Proto: 6, Packets: 1, Bytes: 100}}, 5)
	if res.ByIP[0].Key == "" {
		t.Errorf("IPv6 地址应参与排行: %+v", res.ByIP)
	}
}
