// Package buildinfo 提供程序版本与能力清单。
// 客户端连接后会读取 /api/v1/traffic/now 中的 version / features，
// 据此判断服务端是否支持某项功能（例如抓包导出），并在不支持时给出明确提示，
// 避免出现"点了按钮只报 404"的困惑。
package buildinfo

// Version 是采集端版本号。采集端与客户端各自独立编号（见 AGENTS.md「版本号规则」），
// 两个号段互不相干，不要把这里与 client/ 的版本号对齐。
const Version = "0.23.4"

// Features 列出该版本对外提供的可选能力；新增能力时在此追加。
//
// capture.offline 表示"可离线回放 pcap"（config 的 source=replay + core.offline，
// 或命令行 -replay-file/-offline）：客户端在本机模式下用它判断
// "自己拉起的这个本机引擎能否直接分析本地抓包文件"。
// 注意：本机实时抓包（Windows 原始套接字）尚未实现，因此这里不声明 source.local，
// 避免客户端展示一个点了没反应的能力。
var Features = []string{"history", "sessions", "alerts", "capture.dump", "actions.run", "files", "stream", "topn", "system.upgrade", "capture.multiqueue", "capture.filter", "plugins", "capture.cpupin", "capture.offline"}
