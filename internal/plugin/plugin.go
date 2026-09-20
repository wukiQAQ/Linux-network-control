// Package plugin 实现"界面插件框架"的服务端部分：
// 读取插件目录下的声明式 JSON 清单，校验后通过 GET /api/v1/plugins 下发给客户端渲染。
//
// 插件是纯声明式的（面板标题 + 要拉取的监控接口 + 展示字段），**不含可执行代码**，
// 因此不存在代码注入面；服务端与客户端各做一次校验（widget 类型白名单、endpoint 前缀、字段/列数量上限）。
// 单个文件不合法时只跳过并记录警告，不影响服务启动。
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MaxFileBytes 是单个插件清单的大小上限（防御异常 / 恶意文件）。
	MaxFileBytes = 64 << 10
	// MaxPlugins 是加载的插件数量上限。
	MaxPlugins = 32
	// MaxWidgets 是单个插件的面板块数量上限。
	MaxWidgets = 20
	// MaxFields / MaxColumns 是单个块里的字段 / 列数量上限。
	MaxFields  = 12
	MaxColumns = 12

	// apiPrefix 是插件允许拉取的接口前缀：插件只能读本机监控接口，不能指向任意地址。
	apiPrefix = "/api/v1/"
)

var (
	// idPattern：小写字母开头，仅含小写字母 / 数字 / . _ -，长度 2~32。
	idPattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,31}$`)
	widgetTypes = map[string]bool{"kpi": true, "table": true, "text": true}
	units       = map[string]bool{"": true, "bytes": true, "rate": true, "count": true, "percent": true, "ratio": true}
)

// Field 是 kpi 块里的一个数值字段。
type Field struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Unit  string `json:"unit,omitempty"` // "" | bytes | rate | count | percent | ratio
}

// Column 是 table 块里的一列。
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Unit  string `json:"unit,omitempty"`
}

// Widget 是面板里的一个块：kpi（数值卡）/ table（列表）/ text（说明）。
type Widget struct {
	Type     string            `json:"type"`
	Title    string            `json:"title,omitempty"`
	Text     string            `json:"text,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Root     string            `json:"root,omitempty"` // 列表数据在响应里的字段名，默认 items
	Fields   []Field           `json:"fields,omitempty"`
	Columns  []Column          `json:"columns,omitempty"`
}

// Spec 是一个插件清单。
type Spec struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Icon     string   `json:"icon,omitempty"`
	Order    int      `json:"order,omitempty"`     // 排序：小的排前面
	RefreshS int      `json:"refresh_s,omitempty"` // 自动刷新间隔（秒），0 表示只手动刷新
	Requires []string `json:"requires,omitempty"`  // 需要的服务端能力（features）
	Widgets  []Widget `json:"widgets"`
}

// Load 读取目录下的 *.json 插件清单。
//   - 目录为空字符串表示不启用插件（返回空列表）；
//   - 目录不存在：返回一条警告，不当作错误（多数用户不装插件）；
//   - 单个文件非法：跳过并记录警告，其余插件照常加载。
func Load(dir string) (specs []Spec, warnings []string, err error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, []string{fmt.Sprintf("插件目录不存在: %s", dir)}, nil
		}
		return nil, nil, err
	}
	seen := map[string]string{} // id -> 来源文件，用于重复检测
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		name := e.Name()
		if len(specs) >= MaxPlugins {
			warnings = append(warnings, fmt.Sprintf("%s：插件数量已达上限 %d，已跳过", name, MaxPlugins))
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s：读取失败（%v）", name, err))
			continue
		}
		if len(raw) > MaxFileBytes {
			warnings = append(warnings, fmt.Sprintf("%s：文件超过 %d 字节上限", name, MaxFileBytes))
			continue
		}
		var spec Spec
		if err := json.Unmarshal(raw, &spec); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s：JSON 解析失败（%v）", name, err))
			continue
		}
		if err := spec.Normalize(); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s：%v", name, err))
			continue
		}
		if prev, dup := seen[spec.ID]; dup {
			warnings = append(warnings, fmt.Sprintf("%s：插件 id %q 与 %s 重复，已跳过", name, spec.ID, prev))
			continue
		}
		seen[spec.ID] = name
		specs = append(specs, spec)
	}
	Sort(specs)
	return specs, warnings, nil
}

// Sort 按 order、再按标题 / id 排序，保证客户端展示顺序稳定。
func Sort(specs []Spec) {
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		if specs[i].Title != specs[j].Title {
			return specs[i].Title < specs[j].Title
		}
		return specs[i].ID < specs[j].ID
	})
}

// Normalize 校验清单并补齐默认值；校验失败返回可直接展示给用户的错误。
func (s *Spec) Normalize() error {
	s.ID = strings.TrimSpace(s.ID)
	s.Title = strings.TrimSpace(s.Title)
	s.Icon = strings.TrimSpace(s.Icon)
	if !idPattern.MatchString(s.ID) {
		return fmt.Errorf("id %q 不合法（小写字母开头，仅含小写字母/数字/._-，长度 2~32）", s.ID)
	}
	if s.Title == "" {
		return errors.New("title 不能为空")
	}
	if utf8.RuneCountInString(s.Title) > 40 {
		return errors.New("title 过长（最多 40 个字符）")
	}
	if s.Icon == "" {
		s.Icon = "🧩"
	}
	if s.RefreshS < 0 {
		s.RefreshS = 0
	}
	if s.RefreshS > 600 {
		s.RefreshS = 600
	}
	if len(s.Widgets) == 0 {
		return errors.New("widgets 不能为空")
	}
	if len(s.Widgets) > MaxWidgets {
		return fmt.Errorf("widgets 过多（最多 %d 块）", MaxWidgets)
	}
	reqs := make([]string, 0, len(s.Requires))
	seenReq := map[string]bool{}
	for _, r := range s.Requires {
		r = strings.TrimSpace(r)
		if r == "" || seenReq[r] {
			continue
		}
		seenReq[r] = true
		reqs = append(reqs, r)
	}
	s.Requires = reqs
	for i := range s.Widgets {
		if err := s.Widgets[i].normalize(); err != nil {
			return fmt.Errorf("widgets[%d]：%w", i, err)
		}
	}
	return nil
}

func (w *Widget) normalize() error {
	w.Type = strings.ToLower(strings.TrimSpace(w.Type))
	w.Title = strings.TrimSpace(w.Title)
	if !widgetTypes[w.Type] {
		return fmt.Errorf("type %q 不支持（仅 kpi / table / text）", w.Type)
	}
	if w.Type == "text" {
		w.Text = strings.TrimSpace(w.Text)
		if w.Text == "" {
			return errors.New("text 块必须有 text 内容")
		}
		if utf8.RuneCountInString(w.Text) > 500 {
			return errors.New("text 过长（最多 500 个字符）")
		}
		return nil
	}
	if w.Endpoint == "" {
		if w.Type == "kpi" {
			w.Endpoint = "/api/v1/traffic/now" // kpi 不写 endpoint 时默认取实时指标
		} else {
			return errors.New("table 块必须指定 endpoint")
		}
	}
	if err := checkEndpoint(w.Endpoint); err != nil {
		return err
	}
	if w.Root == "" {
		w.Root = "items"
	}
	if len(w.Params) > 10 {
		return errors.New("params 过多（最多 10 个）")
	}
	for k, v := range w.Params {
		if strings.TrimSpace(k) == "" || len(k) > 32 || len(v) > 128 {
			return fmt.Errorf("params 项 %q 不合法（键 1~32 字符、值最多 128 字符）", k)
		}
	}
	if w.Type == "kpi" {
		if len(w.Fields) == 0 {
			return errors.New("kpi 块必须有 fields")
		}
		if len(w.Fields) > MaxFields {
			return fmt.Errorf("fields 过多（最多 %d 个）", MaxFields)
		}
		for i := range w.Fields {
			f := &w.Fields[i]
			f.Key = strings.TrimSpace(f.Key)
			f.Label = strings.TrimSpace(f.Label)
			f.Unit = strings.ToLower(strings.TrimSpace(f.Unit))
			if f.Key == "" {
				return errors.New("fields[?].key 不能为空")
			}
			if !units[f.Unit] {
				return fmt.Errorf("unit %q 不支持（仅 bytes / rate / count / percent / ratio）", f.Unit)
			}
			if f.Label == "" {
				f.Label = f.Key
			}
		}
		return nil
	}
	// table
	if len(w.Columns) == 0 {
		return errors.New("table 块必须有 columns")
	}
	if len(w.Columns) > MaxColumns {
		return fmt.Errorf("columns 过多（最多 %d 个）", MaxColumns)
	}
	for i := range w.Columns {
		c := &w.Columns[i]
		c.Key = strings.TrimSpace(c.Key)
		c.Label = strings.TrimSpace(c.Label)
		c.Unit = strings.ToLower(strings.TrimSpace(c.Unit))
		if c.Key == "" {
			return errors.New("columns[?].key 不能为空")
		}
		if !units[c.Unit] {
			return fmt.Errorf("unit %q 不支持（仅 bytes / rate / count / percent / ratio）", c.Unit)
		}
		if c.Label == "" {
			c.Label = c.Key
		}
	}
	return nil
}

// checkEndpoint 限制插件只能访问本机监控接口（/api/v1/ 前缀），且不含空白字符。
func checkEndpoint(ep string) error {
	if !strings.HasPrefix(ep, apiPrefix) {
		return fmt.Errorf("endpoint %q 必须以 %s 开头", ep, apiPrefix)
	}
	if strings.ContainsAny(ep, " \t\r\n") || strings.HasPrefix(ep, "/api/v1//") {
		return fmt.Errorf("endpoint %q 含非法字符", ep)
	}
	return nil
}
