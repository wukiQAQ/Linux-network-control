package api

import (
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// TestTopNAPI 校验排行接口：聚合下推到存储层后，TOP IP / 协议名 / 总量仍需正确，
// 且响应里不再出现旧版的 sampled / matched 样本字段（聚合已不受样本上限影响）。
func TestTopNAPI(t *testing.T) {
	ts, store := newTestServer(t)
	now := time.Now().UTC()
	recs := []storage.FlowRecord{
		{Start: now.Add(-time.Minute), End: now, SrcIP: "10.0.0.1", SrcPort: 40000, DstIP: "8.8.8.8", DstPort: 443, Proto: 6, Packets: 10, Bytes: 1500, MachineID: "host-x"},
		{Start: now.Add(-time.Minute), End: now, SrcIP: "10.0.0.2", SrcPort: 40001, DstIP: "1.1.1.1", DstPort: 53, Proto: 17, Packets: 2, Bytes: 200, MachineID: "host-x"},
	}
	for _, rec := range recs {
		if err := store.AppendFlow(rec); err != nil {
			t.Fatal(err)
		}
	}

	var resp struct {
		SinceS     int    `json:"since_s"`
		N          int    `json:"n"`
		TotalFlows int    `json:"total_flows"`
		TotalBytes uint64 `json:"total_bytes"`
		ByIP       []struct {
			Key     string  `json:"key"`
			Bytes   uint64  `json:"bytes"`
			Percent float64 `json:"percent"`
		} `json:"by_ip"`
		ByProto []struct {
			Key   string `json:"key"`
			Bytes uint64 `json:"bytes"`
		} `json:"by_proto"`
		ByPort []struct {
			Key   string `json:"key"`
			Bytes uint64 `json:"bytes"`
		} `json:"by_port"`
	}
	getJSON(t, ts.URL+"/api/v1/topn?since=3600&n=5", &resp)

	if resp.SinceS != 3600 || resp.N != 5 {
		t.Errorf("回显参数错误: since_s=%d n=%d", resp.SinceS, resp.N)
	}
	if resp.TotalFlows != 2 || resp.TotalBytes != 1700 {
		t.Fatalf("总量错误: flows=%d bytes=%d", resp.TotalFlows, resp.TotalBytes)
	}
	// IP 双向计入：两个会话共 4 个端点，第一名是 1500 字节的那一端
	if len(resp.ByIP) != 4 {
		t.Fatalf("TOP IP 应有 4 项（双向计入）: %+v", resp.ByIP)
	}
	if resp.ByIP[0].Bytes != 1500 || resp.ByIP[0].Percent <= 0 {
		t.Errorf("TOP IP 第一名错误: %+v", resp.ByIP[0])
	}
	// 协议名由 topn.ProtoName 映射：6 → TCP、17 → UDP
	if len(resp.ByProto) != 2 || resp.ByProto[0].Key != "TCP" || resp.ByProto[0].Bytes != 1500 {
		t.Fatalf("协议维度错误: %+v", resp.ByProto)
	}
	if resp.ByProto[1].Key != "UDP" || resp.ByProto[1].Bytes != 200 {
		t.Errorf("协议维度第二名应为 UDP: %+v", resp.ByProto[1])
	}
	// 端口同样双向计入：1500 字节那一组里既有目的 443 也有源 40000，两者并列第一
	ports := map[string]bool{}
	for _, e := range resp.ByPort {
		ports[e.Key] = true
		if e.Key == "0" {
			t.Errorf("端口榜不应出现端口 0: %+v", resp.ByPort)
		}
	}
	if len(resp.ByPort) == 0 || resp.ByPort[0].Bytes != 1500 {
		t.Errorf("端口维度第一名应为 1500 字节: %+v", resp.ByPort)
	}
	if !ports["443"] || !ports["40000"] {
		t.Errorf("端口榜应含会话两端的 443 与 40000: %+v", resp.ByPort)
	}

	// 旧版响应里的 sampled / matched 已随"去掉样本上限"一并移除
	var raw map[string]any
	getJSON(t, ts.URL+"/api/v1/topn?since=3600&n=5", &raw)
	for _, gone := range []string{"sampled", "matched"} {
		if _, ok := raw[gone]; ok {
			t.Errorf("响应不应再包含 %s 字段（聚合已下推到存储层）", gone)
		}
	}
	for _, must := range []string{"total_flows", "total_bytes", "by_ip", "by_proto", "by_port"} {
		if _, ok := raw[must]; !ok {
			t.Errorf("响应缺少 %s 字段", must)
		}
	}
}
