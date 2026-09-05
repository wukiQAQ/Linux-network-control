package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
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
