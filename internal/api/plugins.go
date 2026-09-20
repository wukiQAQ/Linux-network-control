package api

import (
	"net/http"

	"github.com/wukiQAQ/Linux-network-control/internal/plugin"
)

// handlePlugins 返回已加载的界面插件清单与加载警告（客户端据此渲染面板）。
// 未配置插件时返回空列表（enabled=false），不返回 404：客户端能区分"服务端不支持"（features
// 里没有 plugins）与"服务端支持但没装插件"，从而给出不同提示。
func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	items := s.plugins
	if items == nil {
		items = []plugin.Spec{}
	}
	warnings := s.pluginWarnings
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  s.pluginsEnabled,
		"total":    len(items),
		"items":    items,
		"warnings": warnings,
		"hint":     "在采集端 config.toml 的 [plugins] dir 里指向插件目录（*.json 声明式清单），重启后生效",
	})
}
