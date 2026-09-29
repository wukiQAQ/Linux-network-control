package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// TestHistoryParamBounds 覆盖代码审查 M5：since/step 必须有界。
//
// 时间轴按 (since/step + 1) 预分配，没有上界时
// ?since=2147483647&step=1 会让一个未鉴权请求直接申请几十 GB 内存。
func TestHistoryParamBounds(t *testing.T) {
	ts, _ := newTestServer(t)

	// 1) 默认窗口：since=3600、step=60 -> 61 个点（与既有 TestHistoryAPI 一致）
	var def struct {
		StepS  int   `json:"step_s"`
		Points []any `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history", &def)
	if len(def.Points) != 61 {
		t.Fatalf("默认窗口点数=%d, want 61", len(def.Points))
	}

	// 2) 窗口被钳到上限：点数 = maxHistorySeconds/step + 1，而不是 2147483648
	var big struct {
		StepS  int   `json:"step_s"`
		Points []any `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history?since=2147483647&step=60", &big)
	want := maxHistorySeconds/60 + 1
	if len(big.Points) != want {
		t.Fatalf("超大 since 的点数=%d, want %d（说明未按 maxHistorySeconds 钳制）", len(big.Points), want)
	}

	// 3) 单步也是有限值：?since=7&step=1 -> 8 个点（若是纳秒误用会得到巨大切片并崩溃）
	var fine struct {
		Points []any `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history?since=7&step=1", &fine)
	if len(fine.Points) != 8 {
		t.Fatalf("since=7&step=1 点数=%d, want 8", len(fine.Points))
	}

	// 4) step > since 时被钳到 since，避免时间轴溢出且只有一个桶
	var stepBig struct {
		StepS  int   `json:"step_s"`
		Points []any `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history?since=3600&step=1000000", &stepBig)
	if stepBig.StepS != 3600 {
		t.Errorf("step 回显=%d, want 3600（应被 since 钳制）", stepBig.StepS)
	}
	if len(stepBig.Points) != 2 {
		t.Errorf("step>since 点数=%d, want 2（t=from 与 t=to）", len(stepBig.Points))
	}

	// 5) since 非正时回落到默认 3600
	var zero struct {
		Points []any `json:"points"`
	}
	getJSON(t, ts.URL+"/api/v1/traffic/history?since=0&step=60", &zero)
	if len(zero.Points) != 61 {
		t.Errorf("since=0 点数=%d, want 61", len(zero.Points))
	}
}

// TestTopNParamBounds 覆盖 /traffic/topn 的 since 上界
// （since 极大时 time.Duration(since)*time.Second 会溢出）。
func TestTopNParamBounds(t *testing.T) {
	ts, _ := newTestServer(t)
	var resp struct {
		SinceS int `json:"since_s"`
	}
	getJSON(t, ts.URL+"/api/v1/topn?since=999999999999&n=5", &resp)
	if resp.SinceS != maxHistorySeconds {
		t.Errorf("since_s=%d, want %d", resp.SinceS, maxHistorySeconds)
	}
	// n 超过上限回落到默认值
	var n struct {
		N int `json:"n"`
	}
	getJSON(t, ts.URL+"/api/v1/topn?n=100000", &n)
	if n.N != 10 {
		t.Errorf("n=%d, want 10", n.N)
	}
}

// TestSessionsParamBounds 覆盖代码审查 M6 与 page_size 下界。
func TestSessionsParamBounds(t *testing.T) {
	ts, store := newTestServer(t)
	now := time.Now().UTC()
	_ = store.AppendFlow(storage.FlowRecord{
		Start: now.Add(-time.Minute), End: now,
		SrcIP: "10.0.0.1", SrcPort: 40000, DstIP: "8.8.8.8", DstPort: 443,
		Proto: 6, Packets: 10, Bytes: 2000, MachineID: "host-x",
	})

	// 1) port=70000 原先被 uint16 静默截断成 4464；现在应视为未过滤（仍能查到会话）
	var overflow struct {
		Total int `json:"total"`
		Items []struct {
			DstPort uint16 `json:"dst_port"`
		} `json:"items"`
	}
	getJSON(t, ts.URL+"/api/v1/sessions?port=70000", &overflow)
	if overflow.Total != 1 || len(overflow.Items) != 1 {
		t.Fatalf("port=70000 不应截断成 4464 去过滤：total=%d", overflow.Total)
	}
	if overflow.Items[0].DstPort != 443 {
		t.Errorf("dst_port=%d, want 443", overflow.Items[0].DstPort)
	}

	// 2) 合法端口仍能正常过滤
	var hit struct {
		Total int `json:"total"`
	}
	getJSON(t, ts.URL+"/api/v1/sessions?port=443", &hit)
	if hit.Total != 1 {
		t.Errorf("port=443 total=%d, want 1", hit.Total)
	}
	// 不匹配的端口返回 0
	var miss struct {
		Total int `json:"total"`
	}
	getJSON(t, ts.URL+"/api/v1/sessions?port=9999", &miss)
	if miss.Total != 0 {
		t.Errorf("port=9999 total=%d, want 0", miss.Total)
	}

	// 3) page_size 下界：0 / 负数不应原样传给存储层
	for _, q := range []string{"page_size=0", "page_size=-5"} {
		var r struct {
			Total int `json:"total"`
		}
		getJSON(t, ts.URL+"/api/v1/sessions?"+q, &r)
		if r.Total != 1 {
			t.Errorf("%s 应回落为默认页大小并返回 1 条，实际 total=%d", q, r.Total)
		}
	}
	// 上界仍是 100
	var capResp struct {
		Total int `json:"total"`
	}
	getJSON(t, ts.URL+"/api/v1/sessions?page_size=99999", &capResp)
	if capResp.Total != 1 {
		t.Errorf("page_size=99999 total=%d, want 1", capResp.Total)
	}
}

// TestFilesSymlinkEscape 覆盖代码审查 H3：白名单目录内的符号链接不得指向外部文件。
//
// 原实现只拒 `..`、用 filepath.Clean 做前缀匹配，再用 os.Stat 跟随链接，
// 因此 /tmp 下放一个指向 /etc/shadow 的链接即可读到白名单外的文件。
func TestFilesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	secret := filepath.Join(base, "secret.txt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.log"), []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := fileRoots
	fileRoots = []string{root}
	defer func() { fileRoots = old }()

	ts, _ := newTestServer(t)

	// 1) 词法上完全落在白名单内的路径，不能因为链接逃逸而放行
	if err := os.Symlink(secret, filepath.Join(root, "evil.txt")); err == nil {
		r, err := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(filepath.Join(root, "evil.txt")))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("下载逃逸符号链接应返回 400，实际 %d", r.StatusCode)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	// 2) 中间段是符号链接：root/linkdir (-> base) /secret.txt
	if err := os.Symlink(base, filepath.Join(root, "linkdir")); err == nil {
		r, err := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(filepath.Join(root, "linkdir", "secret.txt")))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("经中间符号链接逃逸应返回 400，实际 %d", r.StatusCode)
		}
		// 列目录同样要拒
		l, err := http.Get(ts.URL + "/api/v1/files?path=" + url.QueryEscape(filepath.Join(root, "linkdir")))
		if err != nil {
			t.Fatal(err)
		}
		l.Body.Close()
		if l.StatusCode != http.StatusBadRequest {
			t.Errorf("经中间符号链接列目录应返回 400，实际 %d", l.StatusCode)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	// 3) 目录内指向白名单内部的符号链接也应被拒绝（末端是链接一律不开）
	if err := os.Symlink(filepath.Join(root, "ok.log"), filepath.Join(root, "ok-link.log")); err == nil {
		r, err := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(filepath.Join(root, "ok-link.log")))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("末端是符号链接应返回 400，实际 %d", r.StatusCode)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	// 4) 真正的普通文件仍然可下载，内容一致
	r, err := http.Get(ts.URL + "/api/v1/files/download?path=" + url.QueryEscape(filepath.Join(root, "ok.log")))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if r.StatusCode == http.StatusOK {
		body = make([]byte, 64)
		n, _ := r.Body.Read(body)
		body = body[:n]
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK || string(body) != "fine" {
		t.Errorf("普通文件下载被误拒: status=%d body=%q", r.StatusCode, string(body))
	}
}

// TestFilesListDetails 顺带确认列目录的 JSON 形状未因符号链接改造而改变。
func TestFilesListDetails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := fileRoots
	fileRoots = []string{root}
	defer func() { fileRoots = old }()

	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/files?path=" + url.QueryEscape(root))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Path  string `json:"path"`
		Total int    `json:"total"`
		Items []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || out.Total != 1 || out.Items[0].Name != "a.log" {
		t.Fatalf("列目录异常: status=%d out=%+v", resp.StatusCode, out)
	}
}
