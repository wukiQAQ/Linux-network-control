package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/action"
	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/plugin"
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

// actionsTestServer 返回注入了"假执行器"的测试服务，便于在任意平台验证动作接口。
func actionsTestServer(t *testing.T) (*httptest.Server, *action.Executor) {
	t.Helper()
	cfg := config.Default()
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	exec := action.NewExecutorWithRunner(func(ctx context.Context, command string) (string, string, int) {
		return "ok: " + command, "", 0
	})
	srv.SetActions(exec)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { _ = store.Close() })
	t.Cleanup(ts.Close)
	return ts, exec
}

func postJSON(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	r, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer r.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return r.StatusCode, out
}

// TestActionsAPI 覆盖动作目录、执行、危险动作确认、参数校验与审计。
func TestActionsAPI(t *testing.T) {
	ts, _ := actionsTestServer(t)

	// 1) 动作目录：包含命令（供客户端做命令预览）
	var list map[string]any
	getJSON(t, ts.URL+"/api/v1/actions", &list)
	total, _ := list["total"].(float64)
	if total < 15 {
		t.Fatalf("动作数量偏少: %v", total)
	}
	items, _ := list["items"].([]any)
	first, _ := items[0].(map[string]any)
	if _, ok := first["commands"].([]any); !ok {
		t.Errorf("动作目录应包含 commands 字段: %v", first)
	}
	if _, ok := first["category"].(string); !ok {
		t.Errorf("动作目录应包含 category 字段: %v", first)
	}

	// 2) 执行普通动作
	code, res := postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"system.disk"}`)
	if code != http.StatusOK {
		t.Fatalf("执行动作状态码 = %d, resp=%v", code, res)
	}
	if out, _ := res["stdout"].(string); !strings.Contains(out, "df -h") {
		t.Errorf("stdout 应包含实际命令: %v", res["stdout"])
	}
	if cmds, ok := res["commands"].([]any); !ok || len(cmds) != 1 {
		t.Errorf("结果应回带命令列表: %v", res["commands"])
	}

	// 3) 危险动作未确认 -> 409
	code, res = postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"service.restart","params":{"service":"sshd"}}`)
	if code != http.StatusConflict {
		t.Fatalf("未确认的危险动作应返回 409，实际 %d (%v)", code, res)
	}
	// 确认后可以执行
	code, _ = postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"service.restart","params":{"service":"sshd"},"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("确认后应执行成功，实际 %d", code)
	}

	// 4) 参数不合法 -> 400；未知动作 -> 404
	code, _ = postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"service.status","params":{"service":"ssh; rm -rf /"}}`)
	if code != http.StatusBadRequest {
		t.Errorf("非法参数应返回 400，实际 %d", code)
	}
	code, _ = postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"system.rm-rf"}`)
	if code != http.StatusNotFound {
		t.Errorf("未知动作应返回 404，实际 %d", code)
	}

	// 5) 审计历史：前面成功执行了 2 次
	var hist map[string]any
	getJSON(t, ts.URL+"/api/v1/actions/history", &hist)
	if n, _ := hist["total"].(float64); n < 2 {
		t.Errorf("审计历史条数偏少: %v", hist["total"])
	}
}

// TestActionsDisabled 未注入执行器时接口应返回 503。
func TestActionsDisabled(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := postJSON(t, ts.URL+"/api/v1/actions/run", `{"id":"system.disk"}`)
	if code != http.StatusServiceUnavailable {
		t.Errorf("未启用动作接口时应返回 503，实际 %d", code)
	}
}

// TestFilesAPI 覆盖文件通道：目录列表、白名单校验与文件下载（跨平台，用临时目录当白名单根）。
func TestFilesAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("hello-netmon"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := fileRoots
	fileRoots = []string{root}
	defer func() { fileRoots = old }()

	ts, _ := newTestServer(t)
	base := "/api/v1/files?path=" + url.QueryEscape(root)

	// 1) 列目录：目录在前，包含刚创建的文件与子目录
	var list map[string]any
	getJSON(t, ts.URL+base, &list)
	items, _ := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("目录项数量 = %d，期望 2", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "sub" || first["is_dir"] != true {
		t.Errorf("目录应排在最前: %v", first)
	}

	// 2) 白名单外路径 -> 400；含 .. -> 400；不存在 -> 404
	for _, bad := range []string{"/root", "/var/log/../../etc"} {
		r, err := http.Get(ts.URL + "/api/v1/files?path=" + url.QueryEscape(bad))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("非法路径 %s 应返回 400，实际 %d", bad, r.StatusCode)
		}
	}
	r3, _ := http.Get(ts.URL + "/api/v1/files?path=" + url.QueryEscape(filepath.Join(root, "nope")))
	r3.Body.Close()
	if r3.StatusCode != http.StatusNotFound {
		t.Errorf("不存在目录应返回 404，实际 %d", r3.StatusCode)
	}

	// 3) 下载文件：内容一致
	dl, err := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(filepath.Join(root, "app.log")))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != http.StatusOK || string(body) != "hello-netmon" {
		t.Fatalf("下载失败: status=%d body=%q", dl.StatusCode, string(body))
	}

	// 4) 目录不能下载
	d2, _ := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(root))
	d2.Body.Close()
	if d2.StatusCode != http.StatusBadRequest {
		t.Errorf("目录下载应返回 400，实际 %d", d2.StatusCode)
	}
}

// TestStreamAPI 校验实时推送通道：首帧立即到达、事件格式正确、断开可控。
func TestStreamAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream 状态码 = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type 应为 text/event-stream，实际 %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	// 首帧：event: snapshot + data: {...}
	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("读取事件行失败: %v", err)
	}
	if !strings.HasPrefix(line1, "event: snapshot") {
		t.Fatalf("首个事件应为 snapshot，实际 %q", line1)
	}
	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("读取数据行失败: %v", err)
	}
	if !strings.HasPrefix(line2, "data: ") {
		t.Fatalf("第二行应为 data，实际 %q", line2)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line2), "data: ")), &payload); err != nil {
		t.Fatalf("data 不是合法 JSON: %v", err)
	}
	if _, ok := payload["features"]; !ok {
		t.Errorf("实时快照应带 features 字段: %v", payload)
	}
	cancel() // 断开连接后服务端应结束该连接（无泄漏）
}

// 自升级接口：未注入/未启用时返回 503，缺少二次确认返回 409。
func TestUpgradeAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := postJSON(t, ts.URL+"/api/v1/system/upgrade", `{"url":"http://127.0.0.1/x","sha256":"`+strings.Repeat("a", 64)+`"}`)
	if code != http.StatusServiceUnavailable {
		t.Errorf("未启用自升级应返回 503，实际 %d", code)
	}
	// 读状态同样 503
	r, err := http.Get(ts.URL + "/api/v1/system/upgrade")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("状态查询应返回 503，实际 %d", r.StatusCode)
	}
}

// TestFilterFields 校验采集过滤信息随实时指标一起上报（客户端据此提示"服务端正在过滤"）。
func TestFilterFields(t *testing.T) {
	cfg := config.Default()
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	agg := aggregator.New(100)
	table := flow.NewTable(time.Minute, 5*time.Minute, nil)
	srv := New(cfg, agg, table, store, webui.FS)
	calls := 0
	srv.SetFilter("tcp and dst port 443", func() uint64 { calls++; return 7 }, true)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var out map[string]any
	getJSON(t, ts.URL+"/api/v1/traffic/now", &out)
	if out["filter"] != "tcp and dst port 443" {
		t.Errorf("filter = %v，期望规范化后的表达式", out["filter"])
	}
	if v, _ := out["filter_dropped"].(float64); v != 7 {
		t.Errorf("filter_dropped = %v, want 7", out["filter_dropped"])
	}
	if calls == 0 {
		t.Error("应通过注入的计数器读取已过滤帧数")
	}
	if out["filter_kernel"] != true {
		t.Errorf("filter_kernel = %v, want true（客户端据此提示内核剪枝已启用）", out["filter_kernel"])
	}
}

// TestFilterAbsentWhenDisabled 校验未启用过滤时不上报过滤字段（避免客户端误报）。
func TestFilterAbsentWhenDisabled(t *testing.T) {
	ts, _ := newTestServer(t)
	var out map[string]any
	getJSON(t, ts.URL+"/api/v1/traffic/now", &out)
	if _, ok := out["filter"]; ok {
		t.Error("未启用过滤时不应出现 filter 字段")
	}
	if _, ok := out["filter_dropped"]; ok {
		t.Error("未启用过滤时不应出现 filter_dropped 字段")
	}
}

// TestPluginsAPI 校验界面插件接口：未配置时返回空列表 + 可操作提示，配置后按 order 排序下发。
func TestPluginsAPI(t *testing.T) {
	cfg := config.Default()
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := New(cfg, aggregator.New(100), flow.NewTable(time.Minute, 5*time.Minute, nil), store, webui.FS)
	srv.SetPlugins(nil, nil, false)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	type resp struct {
		Enabled  bool          `json:"enabled"`
		Total    int           `json:"total"`
		Items    []plugin.Spec `json:"items"`
		Warnings []string      `json:"warnings"`
		Hint     string        `json:"hint"`
	}
	var out resp
	getJSON(t, ts.URL+"/api/v1/plugins", &out)
	if out.Enabled || out.Total != 0 || len(out.Items) != 0 {
		t.Fatalf("未配置插件时应为空列表: %+v", out)
	}
	if out.Hint == "" {
		t.Error("应给出「如何添加插件」的提示，避免用户以为是 bug")
	}

	late := plugin.Spec{ID: "late", Title: "后加载", Order: 20, Widgets: []plugin.Widget{{Type: "text", Text: "b"}}}
	early := plugin.Spec{ID: "early", Title: "先加载", Order: 10, Widgets: []plugin.Widget{{Type: "text", Text: "a"}}}
	srv.SetPlugins([]plugin.Spec{late, early}, []string{"bad.json：跳过"}, true)
	var out2 resp
	getJSON(t, ts.URL+"/api/v1/plugins", &out2)
	if !out2.Enabled || out2.Total != 2 {
		t.Fatalf("应返回 2 个插件: %+v", out2)
	}
	if out2.Items[0].ID != "early" || out2.Items[1].ID != "late" {
		t.Errorf("应按 order 排序下发（接口层保证顺序稳定）: %+v", out2.Items)
	}
	if len(out2.Warnings) != 1 || out2.Warnings[0] != "bad.json：跳过" {
		t.Errorf("加载警告应原样下发: %+v", out2.Warnings)
	}
}
