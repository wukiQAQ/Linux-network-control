package api

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/webui"
)

func newTestServer(t *testing.T) (*httptest.Server, *storage.FileStore) {
	t.Helper()
	cfg := config.Default()
	cfg.MachineID = "host-x"
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(ts.Close)
	return ts, store
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", url, r.StatusCode)
	}
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("JSON 解码: %v", err)
	}
}

// TestVersionAndFeatures 校验实时指标里带上了版本与能力清单（客户端据此判断功能可用性）。
func TestVersionAndFeatures(t *testing.T) {
	ts, _ := newTestServer(t)
	var out map[string]any
	getJSON(t, ts.URL+"/api/v1/traffic/now", &out)
	if v, _ := out["version"].(string); v == "" {
		t.Error("traffic/now 应包含 version 字段")
	}
	features, ok := out["features"].([]any)
	if !ok || len(features) == 0 {
		t.Fatalf("traffic/now 应包含非空 features 数组，实际 %v", out["features"])
	}
	found := false
	for _, f := range features {
		if f == "capture.dump" {
			found = true
		}
	}
	if !found {
		t.Errorf("features 应包含 capture.dump，实际 %v", features)
	}
}

func TestNowAndHealth(t *testing.T) {
	ts, _ := newTestServer(t)
	var now map[string]any
	getJSON(t, ts.URL+"/api/v1/traffic/now", &now)
	if now["machine_id"] != "host-x" {
		t.Errorf("machine_id=%v", now["machine_id"])
	}
	var health map[string]any
	getJSON(t, ts.URL+"/api/v1/health", &health)
	if _, ok := health["flows_active"]; !ok {
		t.Error("health 缺少 flows_active")
	}
}

func TestHistory(t *testing.T) {
	ts, store := newTestServer(t)
	now := time.Now().UTC()
	_ = store.AppendMetric(storage.MetricPoint{
		Ts: now, Series: "traffic.bps", Value: 12345,
		Tags: map[string]string{"machine_id": "host-x"},
	})
	var resp struct {
		Points []struct {
			Bps float64 `json:"bps"`
		} `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history?since=3600&step=60", &resp)
	if len(resp.Points) != 61 {
		t.Fatalf("点数=%d, want 61", len(resp.Points))
	}
	found := false
	for _, p := range resp.Points {
		if p.Bps == 12345 {
			found = true
		}
	}
	if !found {
		t.Error("历史曲线中未找到写入的指标点")
	}
}

func TestSessionsAPI(t *testing.T) {
	ts, store := newTestServer(t)
	now := time.Now().UTC()
	_ = store.AppendFlow(storage.FlowRecord{
		Start: now.Add(-time.Minute), End: now,
		SrcIP: "10.0.0.1", SrcPort: 40000, DstIP: "8.8.8.8", DstPort: 443,
		Proto: 6, Packets: 10, Bytes: 2000, MachineID: "host-x",
	})
	var resp struct {
		Total int `json:"total"`
		Items []struct {
			SrcIP string `json:"src_ip"`
			Proto string `json:"proto"`
		} `json:"items"`
	}
	getJSON(t, ts.URL+"/api/v1/sessions?proto=tcp&page=1&page_size=10", &resp)
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].Proto != "tcp" {
		t.Fatalf("会话查询异常: %+v", resp)
	}
}

func TestStaticPage(t *testing.T) {
	ts, _ := newTestServer(t)
	r, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	buf := make([]byte, 4096)
	n, _ := r.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "Linux 网络流量监控") {
		t.Error("静态页内容缺失")
	}
}

// authTestServer 返回开启 Bearer 令牌鉴权的测试服务。
func authTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.APIToken = "test-secret"
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(ts.Close)
	return ts
}

func TestAuthRequired(t *testing.T) {
	ts := authTestServer(t)

	// 无令牌访问 API：401
	r, err := http.Get(ts.URL + "/api/v1/traffic/now")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌访问 status=%d, want 401", r.StatusCode)
	}

	// 错误令牌：401
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/traffic/now", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误令牌 status=%d, want 401", r.StatusCode)
	}

	// 正确令牌：200
	req, err = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/traffic/now", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-secret")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("正确令牌 status=%d, want 200", r.StatusCode)
	}

	// 静态页面仍可匿名访问（不含敏感数据）
	r2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("静态页 status=%d, want 200", r2.StatusCode)
	}
}

// alertTestServer 返回已注入告警引擎（含一条 firing 事件）的测试服务。
func alertTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.APIToken = "test-secret"
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	eng := alert.NewWithOptions([]alert.Rule{
		{ID: "bps-high", Series: "traffic.bps", Threshold: 1000, For: 0},
	}, alert.Options{Notify: func(alert.Event) error { return nil }})
	eng.Evaluate(time.Now().UTC(), []alert.Point{{Series: "traffic.bps", Value: 5000}})
	srv.SetAlerts(eng)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(ts.Close)
	return ts
}

func TestAlertsAPI(t *testing.T) {
	ts := alertTestServer(t)
	do := func(query string) map[string]any {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/alerts"+query, nil)
		req.Header.Set("Authorization", "Bearer test-secret")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("alerts status=%d", r.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	firing := do("?status=firing")
	if firing["total"].(float64) != 1 {
		t.Fatalf("firing 总数=%v, want 1", firing["total"])
	}
	all := do("")
	if all["total"].(float64) != 1 {
		t.Fatalf("全部事件数=%v, want 1", all["total"])
	}
	empty := do("?status=resolved")
	if empty["total"].(float64) != 0 {
		t.Fatalf("resolved 应无事件: %v", empty["total"])
	}
}

// captureTestServer 返回已注入按需抓包器的测试服务。
func captureTestServer(t *testing.T) (*httptest.Server, *capture.Dumper) {
	t.Helper()
	cfg := config.Default()
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	d := capture.NewDumper(t.TempDir())
	srv.SetDumper(d)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(ts.Close)
	return ts, d
}

// TestCaptureDumpAPI 覆盖"开始导出 → 查询状态 → 完成 → 下载 pcap"的完整流程。
func TestCaptureDumpAPI(t *testing.T) {
	ts, dumper := captureTestServer(t)

	// 未开始导出时下载应 409
	resp, err := http.Get(ts.URL + "/api/v1/capture/dump/file")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("未导出时下载应返回 409，实际 %d", resp.StatusCode)
	}

	// 开始导出
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/capture/dump?seconds=10", nil)
	startResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var start map[string]any
	if err := json.NewDecoder(startResp.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	startResp.Body.Close()
	if start["active"] != true || start["id"] == "" || start["seconds"] != float64(10) {
		t.Fatalf("开始导出响应异常: %v", start)
	}

	// 模拟管道写入两帧
	dumper.Write(&capture.Packet{Ts: time.Now().UTC(), Raw: []byte{1, 2, 3, 4, 5, 6}})
	dumper.Write(&capture.Packet{Ts: time.Now().UTC(), Raw: []byte{7, 8, 9}})

	var st map[string]any
	getJSON(t, ts.URL+"/api/v1/capture/dump", &st)
	if st["active"] != true || st["packets"] != float64(2) || st["bytes"] != float64(9) {
		t.Fatalf("导出中状态异常: %v", st)
	}

	// 重复开始应 409
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/capture/dump?seconds=10", nil)
	r2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("重复开始应返回 409，实际 %d", r2.StatusCode)
	}

	// 结束导出并下载
	if _, err := dumper.Stop(); err != nil {
		t.Fatal(err)
	}
	var done map[string]any
	getJSON(t, ts.URL+"/api/v1/capture/dump", &done)
	if done["active"] != false || done["ready"] != true || done["packets"] != float64(2) {
		t.Fatalf("结束后状态异常: %v", done)
	}

	fileResp, err := http.Get(ts.URL + "/api/v1/capture/dump/file")
	if err != nil {
		t.Fatal(err)
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode != http.StatusOK {
		t.Fatalf("下载状态 = %d", fileResp.StatusCode)
	}
	body, err := io.ReadAll(fileResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := 24 + 16*2 + 9; len(body) != want {
		t.Errorf("pcap 长度 = %d，期望 %d", len(body), want)
	}
	if len(body) >= 4 && binary.LittleEndian.Uint32(body[0:4]) != 0xa1b2c3d4 {
		t.Errorf("pcap 魔数错误: %x", body[0:4])
	}
	if cd := fileResp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".pcap") {
		t.Errorf("Content-Disposition 异常: %q", cd)
	}
}

// TestCaptureDumpDisabled 未注入抓包器时接口应返回 503 而不是 panic。
func TestCaptureDumpDisabled(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/capture/dump", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("未启用抓包导出时应返回 503，实际 %d", resp.StatusCode)
	}
}
