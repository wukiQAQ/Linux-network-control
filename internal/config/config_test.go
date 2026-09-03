package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := writeTemp(t, `
[core]
machine_id = "host-2"
source = "replay"
tick_ms = 500

[capture]
iface = "eth1"
replay_file = "sample.pcap"
pps = 50

[storage]
data_dir = "/tmp/netmon"
retention_hours = 12

[api]
listen = ":9000"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MachineID != "host-2" || cfg.Source != "replay" || cfg.Iface != "eth1" {
		t.Errorf("基础字段错误: %+v", cfg)
	}
	if cfg.Tick != 500*time.Millisecond {
		t.Errorf("Tick=%v, want 500ms", cfg.Tick)
	}
	if cfg.Retention != 12*time.Hour {
		t.Errorf("Retention=%v, want 12h", cfg.Retention)
	}
	if cfg.Pps != 50 || cfg.DataDir != "/tmp/netmon" || cfg.Listen != ":9000" {
		t.Errorf("扩展字段错误: %+v", cfg)
	}
}

func TestReplayRequiresFile(t *testing.T) {
	p := writeTemp(t, `
[core]
source = "replay"
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "replay_file") {
		t.Errorf("期望 replay_file 校验错误，得到 %v", err)
	}
}

func TestMissingFileReturnsError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("期望文件不存在错误")
	}
}
