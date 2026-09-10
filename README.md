# Linux 网络流量监控系统（Linux Network Control）



:注这是我作为初学者的第一个项目如果有人可以指点一下那么我会非常感谢你的大部分代码使用Codex生成的是作为一个练手项目来进行测试的Vibe Coding
:Note, this is my first project as a beginner. I would be very grateful if anyone could give me some guidance.
 Most of the code was generated using Codex and is used as a practice project to test. Vibe Coding.



运行于 Linux 平台的网络流量监控系统，用于对网络流量进行持续、实时的监测与分析，支持会话级流量查看、指标聚合与异常告警；并配套 Windows 桌面客户端，通过 IP 远程查看 Linux 流量。

## 项目状态

- **当前阶段**：V0.5.3（告警引擎 + 图标/快捷方式 + 应用内多开标签页）
- **技术方向**：Go 语言采集端（AF_PACKET / libpcap）+ Rust/Tauri + Vue 3 桌面客户端（MeTD）
- **架构分层**：采集 → 解析 → 聚合 → 存储 → API → 展示（网页 / 桌面客户端）→ 告警
- **存储**：SQLite（默认，纯 Go 驱动 modernc.org/sqlite），可切换 JSONL（file）
- **数据源**：synthetic 合成 / pcap 回放 / Linux AF_PACKET 真实抓包
- **远程访问**：Linux 端可选 Bearer Token 鉴权；Windows 客户端跨 IP 连接

## 模块整合总览

```text
cmd/netmon/              # Linux 端程序入口：装配各层、HTTP 服务、优雅退出
internal/capture/        # 采集：Source 接口 + synthetic/pcap/live 三种实现
internal/parser/         # 协议解析：以太网 / IPv4 / TCP / UDP / ICMP
internal/flow/           # 会话流表：5 元组双向归并、30s/5min 双超时
internal/aggregator/     # 秒级聚合：bps / pps / 连接数 / 丢包计数
internal/storage/        # Backend 接口 + SQLite 实现（默认）+ JSONL(file)
internal/api/            # REST API /api/v1：now/history/sessions/health（可选 Bearer 鉴权）
internal/webui/          # 内嵌网页仪表盘（go:embed）
internal/app/            # 管道调度：HandlePacket + TickOnce + 主循环
internal/config/         # TOML 配置加载（含 api.token）
client/                  # Windows 桌面客户端 MeTD（Tauri 2 + Vue 3 + ECharts）
  src/                   #   Vue 界面：连接管理 / KPI / 历史曲线 / 会话明细 / 健康
  src-tauri/             #   Rust 侧：reqwest 远程访问、令牌持有、命令层
  test/                  #   前端纯函数单元测试（node --test）
docs/                    # 产品设计 / 技术方案 / 系统设计 / 源码讲解 / 版本记录
demo/index.html          # 纯前端界面演示（模拟数据，评估交互用）
```

## 已实现功能（V0.3）

### Linux 端（Go Agent）
- 实时指标（带宽 bps / 包速率 pps / 并发连接数），1s 轮询刷新
- 会话明细：IP/协议/端口过滤、分页、双向归并统计
- 历史曲线：指标持久化，重启不丢
- 自身健康：收包/丢包数、丢包率、流表大小，丢包页面提示
- 存储：SQLite（WAL、保留策略清理）；pcap 回放全功能可离线测试
- Linux 真实抓包入口（`source = "live"`，需 root / CAP_NET_RAW）
- 可选 Bearer Token 鉴权：`[api] token = "..."` 后，`/api/` 请求需携带 `Authorization: Bearer <token>`

### Windows 桌面客户端 MeTD（client/）
- 连接管理：保存多组服务器配置（地址 + 令牌），本地持久化
- 实时仪表盘：带宽/包速率/并发连接/丢包率 KPI、最近 1 小时历史曲线（ECharts）
- 会话明细：IP 子串过滤、协议筛选、分页查看
- 请求统一经 Rust 命令层（reqwest）访问远程 API，令牌保存在进程状态，规避 CORS
- **V0.4 UI 升级**：程序更名 **MeTD**（蓝色小猫图标）；历史曲线支持折线/面积/柱状三种图表手动切换；设置区提供夜间/明亮主题切换，界面与图表配色随主题变化并本地记忆
- **V0.4.1**：齿轮设置中心（主题/透明度/自定义背景/自启/托盘）、系统托盘常驻、账号一键切换并自动刷新、左侧图标导航（悬停放大+文字提示）

## 路线图

| 版本 | 范围 |
| --- | --- |
| V0.1 MVP ✅ | 全链路闭环（已实现） |
| V0.2 ✅ | SQLite 接入、环境迁移、Linux 连通验证（已实现） |
| V0.3（V1 客户端） ✅ | Windows Tauri 桌面客户端、可选令牌鉴权（已实现） |
| V0.4 ✅ | MeTD：程序更名 + 蓝色小猫图标、三样式统计图切换、夜间/明亮主题（已实现） |
| V0.4.1 ✅ | 齿轮设置中心、系统托盘、账号一键切换、图标导航（已实现） |
| V0.5 ✅ | 自定义应用图标（用户图片去白边），V2 能力线开发开启（已实现） |
| V0.5.1 ✅ | 告警引擎（阈值+持续时长状态机、Webhook、/api/v1/alerts、客户端告警横幅）（已实现） |
| V2 | 告警引擎、TOP N、协议分布、IPv6、多队列抓包、WebSocket、鉴权细化、界面插件框架 |
| V3 | 客户端增强：WebSocket 实时推送、多机对比、Windows 通知、帮助中心型 AI |
| V4 | 流量控制：Linux 端 tc 限速（仅本机流量，默认关闭 + 二次确认）+ 控制 API/界面 |
| V5 | LLM 助手（RAG）、Agent+Server 分布式、NetFlow/sFlow、DPI、eBPF/XDP |

## 快速开始

### Linux 端（同 V0.2）

```powershell
# Windows 演示（synthetic，无需 root）
cd "D:\Codex\Linux network control"
go run ./cmd/netmon
# 浏览器打开 http://localhost:8080
```

Linux 真实抓包/部署步骤见 [使用指南](docs/使用指南.md)。如需开启令牌鉴权，在配置文件中加入：

```toml
[api]
listen = ":8080"
token = "换成你的访问令牌"   # 留空则不鉴权
```

### Windows 桌面客户端（V0.3）

```powershell
cd "D:\Codex\Linux network control\client"
npm install
npx tauri build            # 产物：client/src-tauri/target/release/metd.exe（程序名 MeTD）
# 开发调试：npm run dev 后另开终端执行 npx tauri dev
```

客户端里填写 Linux 的 IP:端口（默认 8080）与令牌即可连接；网页仪表盘能力保持不变。

## 测试

```bash
# Go 端（Linux Agent）
go vet ./... && go test ./...

# 前端纯函数单元测试
cd client && npm test
```

## 文档索引

- [产品设计文档](docs/产品设计文档.md)：产品方向、MVP 功能、验收标准、V1 桌面客户端规划
- [技术方案](docs/技术方案.md)：技术选型与演进架构（含 Tauri 桌面端结论）
- [系统设计文档](docs/网络流量监控系统设计文档.md)：总体架构与模块接口
- [源码讲解](docs/源码讲解.md)：逐模块实现原理与数据流
- [V0.2 运行原理与 SQLite 接入](docs/V0.2-运行原理与SQLite接入.md)：V0.2 版本说明
- [使用指南](docs/使用指南.md)：Windows 演示 / Linux 部署 / 桌面客户端连接
- [更新日志](docs/更新日志.md)：按版本倒序的完整更新说明（含 V0.5.1 告警引擎配置示例）
- [版本记录](docs/版本记录.md)：版本留存与路线图
- [界面演示](demo/index.html)：模拟数据 Demo（浏览器直接打开）