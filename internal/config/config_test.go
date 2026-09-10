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

func TestAlertConfigParsing(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/alert.toml"
	content := `[alert]
enabled = true
bps_threshold = 1000000
bps_for_secs = 15
pps_threshold = 500
pps_for_secs = 10
conns_threshold = 2000
drops_threshold = 50
webhook = "http://127.0.0.1:9000/hook"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Alert.Enabled {
		t.Error("alert.enabled 未解析")
	}
	if cfg.Alert.BpsThreshold != 1_000_000 || cfg.Alert.BpsForSecs != 15 {
		t.Errorf("bps 告警配置异常: %+v", cfg.Alert)
	}
	if cfg.Alert.PpsThreshold != 500 || cfg.Alert.PpsForSecs != 10 {
		t.Errorf("pps 告警配置异常: %+v", cfg.Alert)
	}
	if cfg.Alert.ConnsThreshold != 2000 || cfg.Alert.DropsThreshold != 50 {
		t.Errorf("conns/drops 阈值异常: %+v", cfg.Alert)
	}
	if cfg.Alert.Webhook != "http://127.0.0.1:9000/hook" {
		t.Errorf("webhook 异常: %q", cfg.Alert.Webhook)
	}
	// 未配置时默认不启用
	cfg2 := Default()
	if cfg2.Alert.Enabled {
		t.Error("默认应禁用告警")
	}
	if cfg2.Alert.BpsForSecs != 30 {
		t.Errorf("默认持续秒数=%d, want 30", cfg2.Alert.BpsForSecs)
	}
}
