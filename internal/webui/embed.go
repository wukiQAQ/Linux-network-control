// Package webui 内嵌仪表盘静态页面，随二进制一起发布（单文件部署）。
package webui

import "embed"

//go:embed index.html
var FS embed.FS
