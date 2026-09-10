// Package app 负责把各模块装配成完整的数据管道并驱动运行：
// 采集 → 解析 → 流表 → 聚合 → 存储。
// 拆出 HandlePacket / TickOnce 两个可单测方法，主循环只负责调度。
package app

import (
	"context"
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
	Alerts *alert.Engine // 可选告警引擎，nil 表示不启用
	lastP  time.Time
}

func New(cfg *config.Config, src capture.Source, table *flow.Table, agg *aggregator.Agg, store storage.Backend) *App {
	return &App{Cfg: cfg, Src: src, Table: table, Agg: agg, Store: store}
}

// HandlePacket 处理一个原始报文：解析失败计为一次丢弃，成功则进入流表与聚合。
func (a *App) HandlePacket(p *capture.Packet) bool {
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
	s := a.Agg.Tick(now, a.Table.Active(), a.Table.Created())
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
		"traffic.bps":      s.Bps,
		"traffic.pps":      s.Pps,
		"traffic.conns":    float64(s.Active),
		"traffic.newconns": float64(s.NewConns),
		"traffic.drops":    float64(s.DropEvents),
	}
	for series, v := range vals {
		if err := a.Store.AppendMetric(storage.MetricPoint{
			Ts: s.Ts, Series: series, Value: v,
			Tags: map[string]string{"machine_id": a.Cfg.MachineID},
		}); err != nil {
			log.Printf("[app] 指标落盘失败 series=%s: %v", series, err)
		}
	}
}

// Run 启动采集循环与定时聚合，直到 ctx 取消。
func (a *App) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.Cfg.Tick)
	defer ticker.Stop()
	pkts := make(chan *capture.Packet, 1024)
	go func() {
		defer close(pkts)
		for {
			p, err := a.Src.Next(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[app] 采集源结束: %v", err)
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
				return nil
			}
			a.HandlePacket(p)
		case now := <-ticker.C:
			a.TickOnce(now.UTC())
		}
	}
}
