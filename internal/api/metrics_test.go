package api

import (
	"io"
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

// TestRenderMetrics 校验 Prometheus 文本格式的关键约定：
// HELP/TYPE 成对出现、标签值正确转义、过滤指标只在启用过滤时出现。
func TestRenderMetrics(t *testing.T) {
	snap := metricsSnapshot{
		Machine:  `a"b\c`, // 故意放引号与反斜杠，校验转义
		Version:  "0.22.0",
		Features: []string{"history", "topn"},
		Bps:      1234.5,
		Pps:      12.25,
		Conns:    7,
		NewConns: 3,
		Closed:   2,
		Recv:     1000,
		Drop:     25,
		DropRate: 0.025,
		UptimeS:  60,
		Flows:    9,
	}
	out := renderMetrics(snap)

	wants := []string{
		`netmon_build_info{machine="a\"b\\c",version="0.22.0"} 1`,
		`netmon_feature_enabled{machine="a\"b\\c",feature="topn"} 1`,
		`netmon_traffic_bps{machine="a\"b\\c"} 1234.5`,
		`netmon_traffic_pps{machine="a\"b\\c"} 12.25`,
		`netmon_conns_active{machine="a\"b\\c"} 7`,
		`netmon_drop_rate{machine="a\"b\\c"} 0.025`,
		`netmon_uptime_seconds{machine="a\"b\\c"} 60`,
		`netmon_recv_packets_total{machine="a\"b\\c"} 1000`,
		`# TYPE netmon_recv_packets_total counter`,
		`# TYPE netmon_traffic_bps gauge`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("缺少指标行 %q\n---\n%s", w, out)
		}
	}

	// 未启用过滤时不得输出过滤相关指标（避免误报"正在过滤"）
	if strings.Contains(out, "netmon_filter_") {
		t.Errorf("未启用过滤时不应出现过滤指标:\n%s", out)
	}

	// 每个 # HELP 都必须有同名的 # TYPE（Prometheus 文本格式硬性要求）
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "# HELP ") {
			continue
		}
		name := strings.Fields(line)[2]
		if !strings.Contains(out, "# TYPE "+name+" ") {
			t.Errorf("指标 %s 有 HELP 但缺少 TYPE", name)
		}
	}

	// 启用过滤后：表达式进标签，内核剪枝与过滤计数各一行
	snap.FilterOn = true
	snap.FilterExpr = "tcp and dst port 443"
	snap.FilterKernel = true
	snap.FilterDropped = 7
	on := renderMetrics(snap)
	for _, w := range []string{
		`netmon_filter_info{machine="a\"b\\c",filter="tcp and dst port 443"} 1`,
		`netmon_filter_kernel{machine="a\"b\\c"} 1`,
		`netmon_filter_dropped_total{machine="a\"b\\c"} 7`,
	} {
		if !strings.Contains(on, w) {
			t.Errorf("启用过滤后缺少 %q\n---\n%s", w, on)
		}
	}
	if !strings.Contains(on, `netmon_filter_kernel{machine="a\"b\\c"} 1`) {
		t.Error("filter_kernel=true 时应输出 1")
	}
	snap.FilterKernel = false
	if off := renderMetrics(snap); !strings.Contains(off, `netmon_filter_kernel{machine="a\"b\\c"} 0`) {
		t.Errorf("未启用内核剪枝时应输出 0:\n%s", off)
	}
}

// TestMetricsEndpoint 校验 /metrics 的响应头与内容（未配置 token 时可直接抓取）。
func TestMetricsEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	r, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", r.StatusCode)
	}
	// 版本号写在 Content-Type 里，Prometheus 据此选择解析器
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type=%q，期望 Prometheus 文本格式", ct)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `netmon_build_info{machine="host-x"`) {
		t.Errorf("缺少 build_info 或 machine 标签:\n%s", text)
	}
	if !strings.Contains(text, "netmon_recv_packets_total") {
		t.Errorf("缺少累计收包指标:\n%s", text)
	}
	if strings.Contains(text, "netmon_filter_") {
		t.Errorf("未启用过滤时不应输出过滤指标:\n%s", text)
	}
}

// TestMetricsAuth 校验 /metrics 与 /api/ 一样受 [api] token 保护（Prometheus 用 bearer_token_file）。
func TestMetricsAuth(t *testing.T) {
	ts := authTestServer(t)

	r, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌 status=%d, want 401", r.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
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
		t.Fatalf("带令牌 status=%d, want 200", r.StatusCode)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "netmon_traffic_bps") {
		t.Errorf("带令牌后应返回指标正文:\n%s", string(body))
	}
}

// TestMetricsFilter 校验启用采集过滤时 /metrics 会输出表达式、内核剪枝状态与过滤计数。
func TestMetricsFilter(t *testing.T) {
	cfg := config.Default()
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := New(cfg, aggregator.New(100), flow.NewTable(time.Minute, 5*time.Minute, nil), store, webui.FS)
	srv.SetFilter("tcp and dst port 443", func() uint64 { return 7 }, true)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	r, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, w := range []string{
		`filter="tcp and dst port 443"`,
		"netmon_filter_kernel",
		"netmon_filter_dropped_total",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("启用过滤后缺少 %q:\n%s", w, text)
		}
	}
	if !strings.Contains(text, "netmon_filter_dropped_total") || !strings.Contains(text, " 7") {
		t.Errorf("过滤计数应输出 7:\n%s", text)
	}
}
