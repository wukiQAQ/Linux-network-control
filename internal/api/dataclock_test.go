package api

import (
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/topn"
	"github.com/wukiQAQ/Linux-network-control/internal/webui"
)

// TestDataClockDrivesQueries 覆盖"离线回放的历史 pcap 在界面上看不到数据"这个缺陷：
// 回放时数据时间来自报文时间戳，可能远早于墙钟；查询窗口必须按数据时间取，
// 否则 /api/v1/topn 恒为空、/traffic/now 的 ts 也停留在墙钟，用户会以为数据没落盘。
func TestDataClockDrivesQueries(t *testing.T) {
	dataTime := time.Date(2026, 9, 12, 8, 56, 48, 0, time.UTC)

	store, err := storage.OpenFileStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// 一条会话：结束时间正好是数据时间，起点在 30 秒前。
	if err := store.AppendFlow(storage.FlowRecord{
		Start:   dataTime.Add(-30 * time.Second),
		End:     dataTime,
		SrcIP:   "10.6.203.123",
		SrcPort: 38629,
		DstIP:   "104.18.8.232",
		DstPort: 53,
		Proto:   17,
		Packets: 4,
		Bytes:   1000,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	newSrv := func(clock func() time.Time) *httptest.Server {
		s := New(cfg, aggregator.New(100), flow.NewTable(time.Minute, 5*time.Minute, nil), store, webui.FS)
		s.SetDataClock(clock)
		ts := httptest.NewServer(s.Handler())
		t.Cleanup(ts.Close)
		return ts
	}

	// 1) 数据时钟 = 数据时间：最近 60 秒窗口应当命中这条历史会话。
	ts := newSrv(func() time.Time { return dataTime })
	var res topn.Result
	getJSON(t, ts.URL+"/api/v1/topn?since=60", &res)
	if res.TotalFlows != 1 || res.TotalBytes != 1000 {
		t.Fatalf("数据时钟生效时 TOP 榜应命中历史会话: flows=%d bytes=%d",
			res.TotalFlows, res.TotalBytes)
	}
	ips := map[string]bool{}
	for _, e := range res.ByIP {
		ips[e.Key] = true
	}
	if !ips["10.6.203.123"] || !ips["104.18.8.232"] {
		t.Errorf("会话两端 IP 都应计入 TOP IP: %v", res.ByIP)
	}

	// /traffic/now 的 ts 与 os：ts 取数据时间（客户端据此区分"数据本身的时间"），
	// os 用于让客户端知道自己连的是哪个平台的本机引擎。
	var now struct {
		Ts string `json:"ts"`
		OS string `json:"os"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/now", &now)
	if now.Ts != dataTime.Format(time.RFC3339) {
		t.Errorf("ts 应等于数据时间: got %q want %q", now.Ts, dataTime.Format(time.RFC3339))
	}
	if now.OS != runtime.GOOS {
		t.Errorf("os 应为 %q，实际 %q", runtime.GOOS, now.OS)
	}

	// 2) 反向对照：把数据时钟推到数据之后 1 小时，最近 60 秒窗口内应无数据。
	// 这一条证明窗口确实跟着注入的时钟走，而不是恰好因为墙钟落在数据附近。
	tsFuture := newSrv(func() time.Time { return dataTime.Add(time.Hour) })
	var future topn.Result
	getJSON(t, tsFuture.URL+"/api/v1/topn?since=60", &future)
	if future.TotalFlows != 0 || future.TotalBytes != 0 {
		t.Errorf("窗口右端在数据之后时不应命中数据: flows=%d bytes=%d",
			future.TotalFlows, future.TotalBytes)
	}
}
