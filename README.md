# Linux 网络流量监控系统（Linux Network Control）



:注这是我作为初学者的第一个项目如果有人可以指点一下那么我会非常感谢你的大部分代码使用Codex生成的是作为一个练手项目来进行测试的Vibe Coding
:Note, this is my first project as a beginner. I would be very grateful if anyone could give me some guidance.
 Most of the code was generated using Codex and is used as a practice project to test. Vibe Coding.





运行于 Linux 平台的网络流量监控系统，用于对网络流量进行持续、实时的监测与分析，支持会话级流量查看、指标聚合与异常告警。

## 项目状态

- **当前阶段**：V0.2（MVP 全链路已实现并验证）
- **技术方向**：Go 语言，AF_PACKET / libpcap 采集，分层模块化架构
- **架构分层**：采集 → 解析 → 聚合 → 存储 → API → 展示 → 告警
- **存储**：SQLite（默认，纯 Go 驱动 modernc.org/sqlite），可切换 JSONL（file）
- **数据源**：synthetic 合成 / pcap 回放 / Linux AF_PACKET 真实抓包
- **连通验证**：已在 WSL Ubuntu（Linux 虚拟机）后台运行，Windows 浏览器可访问仪表盘

## 模块整合总览

```text
cmd/netmon/              # 程序入口：装配各层、HTTP 服务、优雅退出
internal/capture/        # 采集：Source 接口 + synthetic/pcap/live 三种实现
internal/parser/         # 协议解析：以太网 / IPv4 / TCP / UDP / ICMP
internal/flow/           # 会话流表：5 元组双向归并、30s/5min 双超时
internal/aggregator/     # 秒级聚合：bps / pps / 连接数 / 丢包计数
internal/storage/        # Backend 接口 + SQLite 实现（默认）+ JSONL(file)
internal/api/            # REST API /api/v1：now/history/sessions/health
internal/webui/          # 仪表盘页面（go:embed 内嵌，真实数据）
internal/app/            # 管道调度：HandlePacket + TickOnce + 主循环
internal/config/         # TOML 配置加载
demo/index.html          # 纯前端界面演示（模拟数据，评估交互用）
docs/                    # 产品设计 / 技术方案 / 系统设计 / 源码讲解 / V0.2
```

## 已实现功能（对照产品设计 MVP）

- 实时指标（带宽 bps / 包速率 pps / 并发连接数），1s 轮询刷新
- 会话明细：IP/协议/端口过滤、分页、双向归并统计
- 历史曲线：指标持久化，重启不丢
- 自身健康：收包/丢包数、丢包率、流表大小，丢包页面提示
- 存储：SQLite（WAL、保留策略清理）；pcap 回放全功能可离线测试
- Linux 真实抓包入口（`source = "live"`，需 root / CAP_NET_RAW）

## 路线图

| 版本 | 范围 |
| --- | --- |
| V0.1 MVP ✅ | 全链路闭环（已实现） |
| V0.2 ✅ | SQLite 接入、环境迁移、Linux 连通验证（已实现） |
| V2 | 告警引擎、TOP N、协议分布、IPv6、多队列抓包、WebSocket、鉴权、界面插件框架 |
| V3 | eBPF/XDP 加速、NetFlow/sFlow 对接、Agent + Server 多机对比、DPI、数据侧插件协议 |

## 快速开始

```bash
# 演示运行（synthetic 数据源 + SQLite，无需 root）
go run ./cmd/netmon
# 浏览器打开 http://localhost:8080

# pcap 回放：编辑 netmon.example.toml 设 source="replay"
# Linux 真实抓包：source="live"，iface="eth0"（root / CAP_NET_RAW）
```

## 测试

```bash
go vet ./... && go test ./...   # 8 个包全绿（含 SQLite、pcap 回放链路测试）
```

## 文档索引

- [产品设计文档](docs/产品设计文档.md)：产品方向、MVP 功能、验收标准
- [技术方案](docs/技术方案.md)：技术选型与演进架构
- [系统设计文档](docs/网络流量监控系统设计文档.md)：总体架构与模块接口
- [源码讲解](docs/源码讲解.md)：逐模块实现原理与数据流
- [V0.2 运行原理与 SQLite 接入](docs/V0.2-运行原理与SQLite接入.md)：V0.2 版本说明
- [使用指南](docs/使用指南.md)：Windows 演示 / pcap 回放 / CentOS 部署
- [界面演示](demo/index.html)：模拟数据 Demo（浏览器直接打开）