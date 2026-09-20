package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadValidAndSort(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "b.json", `{"id":"b-plugin","title":"B 面板","order":20,"widgets":[{"type":"text","text":"说明"}]}`)
	writePlugin(t, dir, "a.json", `{"id":"a-plugin","title":"A 面板","order":10,"widgets":[{"type":"kpi","fields":[{"key":"bps","unit":"rate"}]}]}`)
	// 非 json 文件与子目录应被忽略
	writePlugin(t, dir, "notes.txt", "忽略我")
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	specs, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("不应有警告: %v", warnings)
	}
	if len(specs) != 2 {
		t.Fatalf("插件数=%d, want 2", len(specs))
	}
	if specs[0].ID != "a-plugin" || specs[1].ID != "b-plugin" {
		t.Errorf("应按 order 排序: %q, %q", specs[0].ID, specs[1].ID)
	}
	// kpi 默认取实时指标、label 缺省用 key 补齐、icon 缺省为拼图
	if specs[0].Widgets[0].Endpoint != "/api/v1/traffic/now" {
		t.Errorf("kpi 默认 endpoint=%q", specs[0].Widgets[0].Endpoint)
	}
	if specs[0].Widgets[0].Fields[0].Label != "bps" {
		t.Errorf("label 未补默认值: %q", specs[0].Widgets[0].Fields[0].Label)
	}
	if specs[0].Icon == "" {
		t.Error("icon 应补默认值")
	}
}

func TestLoadSkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "a-ok.json", `{"id":"ok","title":"正常","widgets":[{"type":"text","text":"ok"}]}`)
	writePlugin(t, dir, "badjson.json", `{not json}`)
	writePlugin(t, dir, "badid.json", `{"id":"Bad ID","title":"x","widgets":[{"type":"text","text":"x"}]}`)
	writePlugin(t, dir, "notitle.json", `{"id":"no-title","widgets":[{"type":"text","text":"x"}]}`)
	writePlugin(t, dir, "badtype.json", `{"id":"bad-type","title":"x","widgets":[{"type":"iframe","endpoint":"/api/v1/traffic/now"}]}`)
	writePlugin(t, dir, "noendpoint.json", `{"id":"no-endpoint","title":"x","widgets":[{"type":"table","columns":[{"key":"a"}]}]}`)
	writePlugin(t, dir, "outep.json", `{"id":"out-ep","title":"x","widgets":[{"type":"table","endpoint":"http://evil.example/x","columns":[{"key":"a"}]}]}`)
	writePlugin(t, dir, "z-dup.json", `{"id":"ok","title":"重复","widgets":[{"type":"text","text":"dup"}]}`)
	writePlugin(t, dir, "big.json", `{"id":"big","title":"`+strings.Repeat("x", MaxFileBytes)+`","widgets":[{"type":"text","text":"big"}]}`)

	specs, warnings, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "ok" {
		t.Fatalf("只应加载 ok 插件: %+v", specs)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"badjson.json", "badid.json", "notitle.json", "badtype.json", "noendpoint.json", "outep.json", "z-dup.json", "big.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("警告里应提到 %s：\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "重复") {
		t.Errorf("重复 id 应给出明确原因：\n%s", joined)
	}
	if !strings.Contains(joined, "65536") {
		t.Errorf("超大文件应提示大小上限：\n%s", joined)
	}
}

func TestLoadMissingDirIsOnlyWarning(t *testing.T) {
	specs, warnings, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("目录不存在不应作为致命错误: %v", err)
	}
	if len(specs) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "插件目录不存在") {
		t.Fatalf("specs=%d warnings=%v", len(specs), warnings)
	}
}

func TestLoadDisabledWhenDirEmpty(t *testing.T) {
	specs, warnings, err := Load("   ")
	if specs != nil || warnings != nil || err != nil {
		t.Fatalf("未配置插件目录时应完全静默: %v / %v / %v", specs, warnings, err)
	}
}

func TestNormalizeClampsAndValidates(t *testing.T) {
	s := Spec{
		ID:       "refresh-test",
		Title:    "  刷新测试  ",
		RefreshS: 9999,
		Requires: []string{" topn ", "topn", ""},
		Widgets:  []Widget{{Type: "KPI", Fields: []Field{{Key: "bps"}}}},
	}
	if err := s.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if s.Title != "刷新测试" {
		t.Errorf("title 未去空白: %q", s.Title)
	}
	if s.RefreshS != 600 {
		t.Errorf("refresh_s 应夹取到 600，实际 %d", s.RefreshS)
	}
	if len(s.Requires) != 1 || s.Requires[0] != "topn" {
		t.Errorf("requires 应去重去空: %v", s.Requires)
	}
	if s.Widgets[0].Type != "kpi" {
		t.Errorf("type 应小写归一: %q", s.Widgets[0].Type)
	}

	bad := []Spec{
		{ID: "x", Title: "t", Widgets: []Widget{{Type: "text", Text: "x"}}},                                          // id 太短
		{ID: "ok-id", Title: "", Widgets: []Widget{{Type: "text", Text: "x"}}},                                       // 缺 title
		{ID: "ok-id", Title: "t", Widgets: nil},                                                                      // 缺 widgets
		{ID: "ok-id", Title: "t", Widgets: []Widget{{Type: "text"}}},                                                 // text 无内容
		{ID: "ok-id", Title: "t", Widgets: []Widget{{Type: "kpi", Fields: []Field{{Key: ""}}}}},                      // 字段无 key
		{ID: "ok-id", Title: "t", Widgets: []Widget{{Type: "kpi", Fields: []Field{{Key: "a", Unit: "x"}}}}},          // unit 非法
		{ID: "ok-id", Title: "t", Widgets: []Widget{{Type: "table", Endpoint: "/api/v1/x", Columns: nil}}},           // 表无列
		{ID: "ok-id", Title: "t", Widgets: []Widget{{Type: "table", Endpoint: "/x", Columns: []Column{{Key: "a"}}}}}, // endpoint 越界
	}
	for i, spec := range bad {
		if err := spec.Normalize(); err == nil {
			t.Errorf("第 %d 个非法清单应报错: %+v", i+1, spec)
		}
	}
}
