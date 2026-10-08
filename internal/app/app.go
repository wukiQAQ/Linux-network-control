// Package app 负责把各模块装配成完整的数据管道并驱动运行：
// 采集 → 解析 → 流表 → 聚合 → 存储。
// 拆出 HandlePacket / TickOnce 两个可单测方法，主循环只负责调度。
package app

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/parser"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// App 持有管道各层实例。
type App struct {
	Cfg    *config.Config
	Src    capture.Source
	Table  *flow.Table
	Agg    *aggregator.Agg
	Store  storage.Backend
	Alerts *alert.Engine   // 可选告警引擎，nil 表示不启用
	Dumper *capture.Dumper // 可选按需抓包器，nil 表示不导出 pcap
	// Offline 为真时走离线回放路径：用报文时间戳（而非墙钟）驱动聚合与过期，
	// 且数据源耗尽后不退出进程，直到 ctx 取消。默认由 cfg.Offline 初始化。
	Offline bool
	lastP   time.Time
	// dataTs 是"当前数据时间"：离线回放时来自 pcap 报文时间戳（见 offlineClock），
	// 在线模式等于墙钟；未产生数据时为零值。API 查询窗口以 DataNow 为准。
	dataTs time.Time
}

func New(cfg *config.Config, src capture.Source, table *flow.Table, agg *aggregator.Agg, store storage.Backend) *App {
	return &App{Cfg: cfg, Src: src, Table: table, Agg: agg, Store: store, Offline: cfg.Offline}
}

// HandlePacket 处理一个原始报文：解析失败计为一次丢弃，成功则进入流表与聚合。
func (a *App) HandlePacket(p *capture.Packet) bool {
	// 数据时间跟随报文时间戳：离线回放的数据时间可能远早于墙钟，
	// 先于第一个聚合周期更新一次，查询窗口才不会落在数据之前。
	if p.Ts.After(a.dataTs) {
		a.dataTs = p.Ts
	}
	if a.Dumper != nil {
		// 按需抓包：把原始报文写入 pcap，供 Wireshark 等工具分析
		a.Dumper.Write(p)
	}
	info, err := parser.Parse(p.Raw)
	if err != nil {
		a.Agg.RecordDrop()
		return false
	}
	a.Table.Handle(info.Protocol, info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, info.TCPFlags, len(p.Raw), p.Ts)
	a.Agg.Observe(len(p.Raw))
	return true
}

// TickOnce 执行一个聚合周期：输出采样、过期会话落盘、指标持久化、按策略清理。
func (a *App) TickOnce(now time.Time) aggregator.Sample {
	if now.After(a.dataTs) {
		a.dataTs = now
	}
	s := a.Agg.Tick(now, a.Table.Active(), a.Table.Created(), a.Table.Closed())
	for _, f := range a.Table.Expire(now) {
		if err := a.Store.AppendFlow(a.toRecord(f)); err != nil {
			log.Printf("[app] 流记录落盘失败: %v", err)
		}
	}
	a.persistMetrics(s)
	a.evaluateAlerts(now, s)
	if now.Sub(a.lastP) >= a.Cfg.Retention/2 || a.lastP.IsZero() {
		a.lastP = now
		if err := a.Store.Prune(now); err != nil {
			log.Printf("[app] 数据清理失败: %v", err)
		}
	}
	return s
}

// DataNow 返回"当前数据时间"：离线回放时是最后一个报文/聚合周期的数据时间，
// 在线模式即墙钟；尚未产生任何数据时退回墙钟。
//
// 为什么需要它：离线回放的历史 pcap，其数据时间可能远早于墙钟。API 层若一律用
// time.Now() 取"最近 N 秒"，回放数据会整体落在窗口之外——TOP 榜与历史曲线恒为空，
// 用户会以为"抓包数据没进去"。
func (a *App) DataNow() time.Time {
	if a.dataTs.IsZero() {
		return time.Now().UTC()
	}
	return a.dataTs.UTC()
}

// evaluateAlerts 用本次采样评估告警规则并记录产生的事件。
func (a *App) evaluateAlerts(now time.Time, s aggregator.Sample) {
	if a.Alerts == nil {
		return
	}
	pts := []alert.Point{
		{Series: "traffic.bps", Value: s.Bps},
		{Series: "traffic.pps", Value: s.Pps},
		{Series: "traffic.conns", Value: float64(s.Active)},
		{Series: "traffic.drops", Value: float64(s.DropEvents)},
	}
	for _, ev := range a.Alerts.Evaluate(now, pts) {
		log.Printf("[alert] %s %s rule=%s value=%.0f threshold=%.0f",
			ev.Status, ev.Series, ev.RuleID, ev.Value, ev.Threshold)
		// 告警联动：触发时自动导出抓包，便于事后用 Wireshark 分析
		if ev.Status == "firing" && a.Cfg.Alert.AutoCapture && a.Dumper != nil {
			secs := a.Cfg.Alert.AutoCaptureSeconds
			if secs <= 0 {
				secs = 15
			}
			if res, err := a.Dumper.Start(secs); err != nil {
				log.Printf("[alert] 自动抓包未启动: %v", err)
			} else {
				log.Printf("[alert] 已自动开始抓包 %d 秒（规则 %s），结束后可在客户端下载", res.Seconds, ev.RuleID)
			}
		}
	}
}

func (a *App) toRecord(f flow.Flow) storage.FlowRecord {
	return storage.FlowRecord{
		Start:     f.Start,
		End:       f.End,
		SrcIP:     f.SrcIP.String(),
		SrcPort:   f.SPort,
		DstIP:     f.DstIP.String(),
		DstPort:   f.DPort,
		Proto:     f.Key.Proto,
		Packets:   f.Packets,
		Bytes:     f.Bytes,
		TCPFlags:  f.TCPFlags,
		Iface:     a.Cfg.Iface,
		MachineID: a.Cfg.MachineID,
	}
}

func (a *App) persistMetrics(s aggregator.Sample) {
	vals := map[string]float64{
		"traffic.bps":         s.Bps,
		"traffic.pps":         s.Pps,
		"traffic.conns":       float64(s.Active),
		"traffic.newconns":    float64(s.NewConns),
		"traffic.closedconns": float64(s.Closed),
		"traffic.drops":       float64(s.DropEvents),
	}
	points := make([]storage.MetricPoint, 0, len(vals))
	for series, v := range vals {
		points = append(points, storage.MetricPoint{
			Ts: s.Ts, Series: series, Value: v,
			Tags: map[string]string{"machine_id": a.Cfg.MachineID},
		})
	}
	// 批量写入：每周期 6 个指标一次事务/一次刷盘
	if err := a.Store.AppendMetrics(points); err != nil {
		log.Printf("[app] 指标批量落盘失败: %v", err)
	}
}

// Run 启动采集循环与聚合调度，直到 ctx 取消。
// 在线模式（Offline=false）用墙钟 ticker 驱动；离线回放模式用报文时间戳驱动数据时钟，
// 且数据源耗尽后进程不退出（见 runOffline）。
func (a *App) Run(ctx context.Context) error {
	if a.Offline {
		return a.runOffline(ctx)
	}
	return a.runOnline(ctx)
}

// offlineClock 是离线回放专用的"数据时钟"：时间基准来自报文时间戳（数据时间），
// 而不是墙钟。这样流表过期、聚合采样、落盘时间戳都落在 pcap 的时间区间内，
// 历史回放（几毫秒就跑完）也能产生完整的秒级曲线。
type offlineClock struct {
	firstPkt time.Time // 首个报文时间戳
	lastTick time.Time // 上一次 tick 使用的数据时间；零值表示尚未建立基线
	lastPkt  time.Time // 已读到的最新报文时间戳
}

// advance 用新报文时间戳 pkt 推进数据时钟：
// ok=false 表示距上次 tick 的数据时间前进不足 tick，本轮不聚合；
// ok=true 时返回本次 tick 应使用的数据时间（对齐到 lastTick+tick 的周期边界，且不越过 pkt）。
func (c *offlineClock) advance(pkt time.Time, tick time.Duration) (time.Time, bool) {
	if pkt.IsZero() || tick <= 0 {
		return time.Time{}, false
	}
	if c.firstPkt.IsZero() {
		c.firstPkt = pkt
	}
	c.lastPkt = pkt
	if c.lastTick.IsZero() {
		c.lastTick = pkt // 首帧只建立基线，不产生一条空采样
		return time.Time{}, false
	}
	if pkt.Sub(c.lastTick) < tick {
		return time.Time{}, false
	}
	now := c.lastTick.Add(tick)
	if now.After(pkt) {
		now = pkt
	}
	c.lastTick = now
	return now, true
}

// final 返回数据源耗尽时的收尾 tick 时间（即最后一个报文的数据时间），
// 用于把最后一个采样与残留流落盘；一个报文都没有时 ok=false。
func (c *offlineClock) final() (time.Time, bool) {
	if c.lastPkt.IsZero() {
		return time.Time{}, false
	}
	if c.lastPkt.After(c.lastTick) {
		return c.lastPkt, true
	}
	return c.lastTick, true
}

// runOffline 离线回放循环：逐包推进数据时钟并按需聚合，源耗尽后补一次收尾 tick，
// 把残留会话落盘，然后阻塞等待 ctx 取消——进程保持存活，用户可继续查询已落盘的结果。
func (a *App) runOffline(ctx context.Context) error {
	clock := &offlineClock{}
	packets := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		p, err := a.Src.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !errors.Is(err, io.EOF) {
				log.Printf("[app] 离线回放读取中断: %v", err)
			}
			break
		}
		if now, ok := clock.advance(p.Ts, a.Cfg.Tick); ok {
			a.TickOnce(now)
		}
		a.HandlePacket(p)
		packets++
	}
	// 收尾：写出最后一个采样（含末尾未聚合的报文）
	if now, ok := clock.final(); ok {
		a.TickOnce(now)
	}
	// 数据时间刚结束、尚未达到空闲超时的活跃会话也一并落盘，保证离线结果完整
	rest := a.flushRemaining()
	log.Printf("[app] 离线回放完成: 报文=%d 残留会话落盘=%d 数据时间=[%s, %s]；HTTP 服务继续可用，等待退出信号",
		packets, rest, clock.firstPkt.Format(time.RFC3339), clock.lastPkt.Format(time.RFC3339))
	<-ctx.Done()
	return nil
}

// flushRemaining 把流表中尚未过期的会话强制落盘，返回落盘条数。
// 仅在离线回放结束时调用（此后不再 tick，不会重复落盘）。
func (a *App) flushRemaining() int {
	flows := a.Table.Snapshot()
	for _, f := range flows {
		if err := a.Store.AppendFlow(a.toRecord(f)); err != nil {
			log.Printf("[app] 流记录落盘失败: %v", err)
		}
	}
	return len(flows)
}

// runOnline 在线采集循环：墙钟 ticker 驱动聚合，数据源结束即排空返回。
// 数据源意外报错（非 EOF / 非 ctx 取消）时把错误上抛给 main。
func (a *App) runOnline(ctx context.Context) error {
	ticker := time.NewTicker(a.Cfg.Tick)
	defer ticker.Stop()
	pkts := make(chan *capture.Packet, 1024)
	srcErr := make(chan error, 1)
	go func() {
		defer close(pkts)
		for {
			p, err := a.Src.Next(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[app] 采集源结束: %v", err)
					if !errors.Is(err, io.EOF) {
						select {
						case srcErr <- err:
						default:
						}
					}
				}
				return
			}
			select {
			case pkts <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-pkts:
			if !ok {
				// 排空缓冲中剩余的报文后再退出（回放文件结束场景）
				for p := range pkts {
					a.HandlePacket(p)
				}
				log.Printf("[app] 数据源已关闭，管道排空完毕")
				select {
				case err := <-srcErr:
					return err
				default:
					return nil
				}
			}
			a.HandlePacket(p)
		case now := <-ticker.C:
			a.TickOnce(now.UTC())
		}
	}
}
