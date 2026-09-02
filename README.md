# Linux 网络流量监控系统（Linux Network Control）



:注这是我作为初学者的第一个项目如果有人可以指点一下那么我会非常感谢你的大部分代码使用Codex生成的是作为一个练手项目来进行测试的Vibe Coding
:Note, this is my first project as a beginner. I would be very grateful if anyone could give me some guidance.
 Most of the code was generated using Codex and is used as a practice project to test. Vibe Coding.





运行于 Linux 平台的网络流量监控系统，用于对网络流量进行持续、实时的监测与分析，支持会话级流量查看、指标聚合与异常告警。

## 项目状态

- **当前阶段**：架构设计阶段
- **技术方向**：Go 语言，AF_PACKET / libpcap 采集，分层模块化架构
- **架构分层**：采集 → 解析 → 聚合 → 存储 → API → 展示 → 告警

## 目录结构

```text
.
├── docs/                    # 设计文档
├── README.md
└── AGENTS.md                # 仓库工作流规范
```

## 文档

- [系统设计文档](docs/网络流量监控系统设计文档.md)：总体架构、模块设计、接口定义、实现方案与里程碑

## 路线图

1. MVP：抓包 → 解析 → 指标 → API → 展示全链路跑通
2. 完整功能：多队列采集、TOP N、协议分布、告警引擎
3. 性能与扩展：eBPF 加速、分布式采集、DPI 应用识别

详细规划见设计文档第 7 章。
