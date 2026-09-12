package webui

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEmbeddedDashboardAssets 校验内嵌仪表盘资源可用，
// 并且包含「图表类型切换」与「缩放」这两项界面能力的关键实现标记。
func TestEmbeddedDashboardAssets(t *testing.T) {
	data, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatalf("读取内嵌 index.html 失败: %v", err)
	}
	html := string(data)
	if len(html) < 1000 {
		t.Fatalf("内嵌页面内容异常，长度=%d", len(html))
	}
	wants := []struct {
		marker string
		desc   string
	}{
		{`id="modeSeg"`, "图表类型切换控件"},
		{`data-m="line"`, "折线图按钮"},
		{`data-m="area"`, "面积图按钮"},
		{"function setChartMode", "图表类型切换函数"},
		{`id="zoomSeg"`, "缩放控件"},
		{`id="zoomInfo"`, "缩放比例提示"},
		{"function zoomChart", "缩放函数"},
		{"function visiblePoints", "缩放区间裁剪"},
		{`id="rangeSeg"`, "时间范围控件（与图表类型分离）"},
	}
	for _, w := range wants {
		if !strings.Contains(html, w.marker) {
			t.Errorf("内嵌仪表盘缺少 %s（%s）", w.desc, w.marker)
		}
	}
	// 柱状图已按需求移除，页面不应再出现相关标记。
	if strings.Contains(html, `data-m="bar"`) || strings.Contains(html, `chartMode === "bar"`) {
		t.Error("内嵌仪表盘仍存在柱状图相关实现")
	}
	// 时间范围与图表类型是两组独立控件，切换时间范围不能清掉图表类型的选中态。
	if strings.Contains(html, `document.querySelectorAll(".seg button")`) {
		t.Error("setRange 仍在使用全局 .seg 选择器，会误清图表类型按钮状态")
	}
	if !strings.Contains(html, `document.querySelectorAll("#rangeSeg button")`) {
		t.Error("setRange 未限定在 #rangeSeg 范围内")
	}
}
