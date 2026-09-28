package main

import (
	"testing"

	"github.com/wukiQAQ/Linux-network-control/internal/config"
)

// TestApplyCLIOverrides 校验命令行覆盖语义：
// -replay-file 非空时覆盖 capture.replay_file 并把数据源切到 replay（否则会造包空跑）；
// -offline 为真时开启离线模式，未给出（false）时不得把配置文件里已开启的离线模式关掉。
func TestApplyCLIOverrides(t *testing.T) {
	cfg := config.Default()
	if cfg.Source != "synthetic" {
		t.Fatalf("前提不成立：Default() 的数据源应为 synthetic，实际 %q", cfg.Source)
	}
	applyCLIOverrides(cfg, "", false)
	if cfg.ReplayFile != "" || cfg.Offline || cfg.Source != "synthetic" {
		t.Fatalf("未给参数时不应改变默认配置: replay=%q offline=%v source=%q",
			cfg.ReplayFile, cfg.Offline, cfg.Source)
	}

	applyCLIOverrides(cfg, "dump.pcap", true)
	if cfg.ReplayFile != "dump.pcap" {
		t.Fatalf("-replay-file 未覆盖: %q", cfg.ReplayFile)
	}
	if cfg.Source != "replay" {
		t.Fatalf("-replay-file 必须同时把数据源切到 replay，实际 %q（否则会静默造包空跑）", cfg.Source)
	}
	if !cfg.Offline {
		t.Fatal("-offline 未开启离线模式")
	}

	// 配置文件里已开启离线，命令行未显式给出时保持开启
	cfg2 := config.Default()
	cfg2.Offline = true
	applyCLIOverrides(cfg2, "", false)
	if !cfg2.Offline {
		t.Error("命令行未给出 -offline 时不应关闭配置里的离线模式")
	}
	if cfg2.Source != "synthetic" {
		t.Errorf("未给 -replay-file 时不应改数据源: %q", cfg2.Source)
	}
}
