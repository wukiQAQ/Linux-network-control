// Package buildinfo 提供程序版本与能力清单。
// 客户端连接后会读取 /api/v1/traffic/now 中的 version / features，
// 据此判断服务端是否支持某项功能（例如抓包导出），并在不支持时给出明确提示，
// 避免出现"点了按钮只报 404"的困惑。
package buildinfo

// Version 是采集端版本号，与客户端保持同一主线。
const Version = "0.7.1"

// Features 列出该版本对外提供的可选能力；新增能力时在此追加。
var Features = []string{"history", "sessions", "alerts", "capture.dump"}
