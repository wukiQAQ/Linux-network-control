// netmon 是 Linux 网络流量监控系统的入口程序。
// 职责：加载配置 → 选择数据源 → 装配各层 → 启动 HTTP 服务与采集循环。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/api"
	"github.com/wukiQAQ/Linux-network-control/internal/app"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/webui"
)

func main() {
	configPath := flag.String("config", "config.toml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.Default()
		log.Printf("[config] 未找到 %s，使用默认配置（synthetic 数据源）", *configPath)
	} else if err != nil {
		log.Fatalf("[config] 配置加载失败: %v", err)
	}
	log.Printf("[config] machine=%s source=%s iface=%s tick=%v listen=%s",
		cfg.MachineID, cfg.Source, cfg.Iface, cfg.Tick, cfg.Listen)

	src, err := buildSource(cfg)
	if err != nil {
		log.Fatalf("[capture] 数据源创建失败: %v", err)
	}
	defer src.Close()

	table := flow.NewTable(30*time.Second, 5*time.Minute, nil)
	agg := aggregator.New(3600)
	var store storage.Backend
	if cfg.Storage == "file" {
		store, err = storage.OpenFileStore(cfg.DataDir, cfg.Retention)
	} else {
		store, err = storage.OpenSQLiteStore(cfg.SQLitePath, cfg.Retention)
	}
	if err != nil {
		log.Fatalf("[storage] 存储初始化失败: %v", err)
	}
	defer store.Close()

	eng := buildAlertEngine(cfg)
	// 按需抓包：导出 pcap 供 Wireshark 分析（文件放在数据目录的 dumps 子目录）
	dumper := capture.NewDumper(filepath.Join(cfg.DataDir, "dumps"))
	a := app.New(cfg, src, table, agg, store)
	a.Alerts = eng
	a.Dumper = dumper
	srv := api.New(cfg, agg, table, store, webui.FS)
	srv.SetAlerts(eng)
	srv.SetDumper(dumper)
	handler := srv.Handler()
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("[api] HTTP 服务启动: http://localhost%s", cfg.Listen)
		serveErr <- httpSrv.ListenAndServe()
	}()

	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[api] HTTP 服务异常退出: %v", err)
		}
	case err := <-runErr:
		if err != nil {
			log.Printf("[app] 采集循环退出: %v", err)
		}
	case <-ctx.Done():
		log.Printf("[main] 收到退出信号，正在关闭…")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Printf("[main] 已退出")
}

// buildAlertEngine 按配置构建告警引擎；未启用或无有效阈值规则时返回 nil。
func buildAlertEngine(cfg *config.Config) *alert.Engine {
	if !cfg.Alert.Enabled {
		return nil
	}
	rules := []alert.Rule{}

	if cfg.Alert.BpsThreshold > 0 {
		rules = append(rules, alert.Rule{ID: "bps-high", Series: "traffic.bps", Threshold: cfg.Alert.BpsThreshold, For: time.Duration(cfg.Alert.BpsForSecs) * time.Second})
	}
	if cfg.Alert.PpsThreshold > 0 {
		rules = append(rules, alert.Rule{ID: "pps-high", Series: "traffic.pps", Threshold: cfg.Alert.PpsThreshold, For: time.Duration(cfg.Alert.PpsForSecs) * time.Second})
	}
	if cfg.Alert.ConnsThreshold > 0 {
		rules = append(rules, alert.Rule{ID: "conns-high", Series: "traffic.conns", Threshold: cfg.Alert.ConnsThreshold, For: time.Duration(cfg.Alert.ConnsForSecs) * time.Second})
	}
	if cfg.Alert.DropsThreshold > 0 {
		rules = append(rules, alert.Rule{ID: "drops-high", Series: "traffic.drops", Threshold: cfg.Alert.DropsThreshold, For: time.Duration(cfg.Alert.DropsForSecs) * time.Second})
	}
	if len(rules) == 0 {
		log.Printf("[alert] 已启用但未配置有效阈值规则，跳过")
		return nil
	}
	log.Printf("[alert] 启用 %d 条规则（webhook=%s）", len(rules), cfg.Alert.Webhook)
	return alert.New(rules, cfg.Alert.Webhook)
}

// buildSource 按配置创建数据源；live 模式仅在 Linux 上可用。
func buildSource(cfg *config.Config) (capture.Source, error) {
	switch cfg.Source {
	case "synthetic":
		return capture.NewSynthetic(cfg.Pps, cfg.Iface), nil
	case "replay":
		return capture.OpenPcapFile(cfg.ReplayFile)
	case "live":
		if runtime.GOOS != "linux" {
			return nil, errors.New("live 抓包仅支持 Linux（当前系统 " + runtime.GOOS + "，可改用 synthetic/replay）")
		}
		return capture.NewLive(cfg.Iface)
	default:
		return nil, errors.New("未知数据源: " + cfg.Source + "（可选 synthetic/replay/live）")
	}
}
