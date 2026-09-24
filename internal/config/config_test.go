package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/filter"
	"github.com/wukiQAQ/Linux-network-control/internal/plugin"
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

// TestFilterConfigParsing 校验 capture.filter 的解析，并用过滤层解析器确认表达式可用
// （表达式非法时服务端启动即报错，不会静默地"不过滤"）。
func TestFilterConfigParsing(t *testing.T) {
	p := writeTemp(t, `
[capture]
filter = "tcp and (host 10.0.0.5 or net 192.168.0.0/16)"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "tcp and (host 10.0.0.5 or net 192.168.0.0/16)"
	if cfg.Filter != want {
		t.Fatalf("capture.filter 未解析: %q", cfg.Filter)
	}
	flt, err := filter.Parse(cfg.Filter)
	if err != nil {
		t.Fatalf("capture.filter 无法解析: %v", err)
	}
	if flt.String() != want {
		t.Errorf("规范化结果=%q, want %q", flt.String(), want)
	}
	if d := Default(); d.Filter != "" {
		t.Errorf("默认 filter=%q，期望空（不过滤）", d.Filter)
	}
	if _, err := filter.Parse("vlan"); err == nil {
		t.Error("非法表达式应报错")
	}
}

// TestPluginConfigParsing 校验 [plugins] dir 解析，并串起"配置 → 插件加载"这条链路。
func TestPluginConfigParsing(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, "[plugins]\ndir = \""+dir+"\"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Plugins.Dir != dir {
		t.Fatalf("plugins.dir=%q, want %q", cfg.Plugins.Dir, dir)
	}
	if d := Default(); d.Plugins.Dir != "" {
		t.Errorf("默认 plugins.dir=%q，期望空（不加载插件）", d.Plugins.Dir)
	}
	// 目录里放一个合法插件，走一遍真实加载
	spec := `{"id":"cfg-demo","title":"配置示例","widgets":[{"type":"text","text":"ok"}]}`
	if err := os.WriteFile(filepath.Join(dir, "demo.json"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	specs, warnings, err := plugin.Load(cfg.Plugins.Dir)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Load 插件: %v / %v", err, warnings)
	}
	if len(specs) != 1 || specs[0].ID != "cfg-demo" {
		t.Fatalf("应加载 1 个插件: %+v", specs)
	}
}

// TestCaptureCPUPinParsing 校验 capture.readers 与 capture.cpu_pin 的解析。
func TestCaptureCPUPinParsing(t *testing.T) {
	p := writeTemp(t, "[capture]\nreaders = 4\ncpu_pin = true\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Readers != 4 || !cfg.CPUPin {
		t.Fatalf("readers=%d cpu_pin=%v, want 4/true", cfg.Readers, cfg.CPUPin)
	}
	if d := Default(); d.CPUPin {
		t.Error("默认应关闭 CPU 绑定（保持原有调度行为）")
	}
}
