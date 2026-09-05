// Package config 负责加载系统配置。
// MVP 使用 TOML 的轻量子集：支持 [section] 与 key = value（字符串/整数/布尔），
// 未知字段忽略，缺失字段使用默认值，便于后续扩展配置项。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 汇总各模块需要的运行参数。
type Config struct {
	MachineID  string        // 机器标识，未来多机对比时的标签
	Source     string        // 数据源：synthetic | replay | live
	Iface      string        // 监控网卡（live 模式使用）
	Filter     string        // BPF 过滤表达式（预留，MVP 尚未实现）
	ReplayFile string        // pcap 回放文件路径
	Pps        int           // synthetic 数据源每秒产生包数
	Tick       time.Duration // 指标聚合周期
	DataDir    string        // 数据存储目录
	Storage    string        // 存储后端：sqlite | file
	SQLitePath string        // sqlite 数据库文件路径
	Retention  time.Duration // 数据保留时长
	Listen     string        // HTTP 监听地址
	APIToken   string        // 可选 API 访问令牌（Bearer Token），空表示不鉴权
}

// Default 返回开箱即用的默认配置（synthetic 数据源，便于无网卡环境演示）。
func Default() *Config {
	return &Config{
		MachineID:  "host-1",
		Source:     "synthetic",
		Iface:      "eth0",
		Pps:        200,
		Tick:       time.Second,
		DataDir:    "data",
		Storage:    "sqlite",
		SQLitePath: "data/netmon.db",
		Retention:  24 * time.Hour,
		Listen:     ":8080",
	}
}

// Load 从 TOML 文件加载配置，解析失败时返回错误。
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	values := map[string]string{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if section != "" {
			key = section + "." + key
		}
		values[key] = unquote(val)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	cfg := Default()
	cfg.apply(values)
	if cfg.Source == "replay" && cfg.ReplayFile == "" {
		return nil, fmt.Errorf("source=replay 时必须配置 replay_file")
	}
	return cfg, nil
}

func (c *Config) apply(v map[string]string) {
	if s, ok := v["core.machine_id"]; ok {
		c.MachineID = s
	}
	if s, ok := v["core.source"]; ok {
		c.Source = s
	}
	if s, ok := v["capture.iface"]; ok {
		c.Iface = s
	}
	if s, ok := v["capture.filter"]; ok {
		c.Filter = s
	}
	if s, ok := v["capture.replay_file"]; ok {
		c.ReplayFile = s
	}
	if n, ok := v["capture.pps"]; ok {
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			c.Pps = i
		}
	}
	if n, ok := v["core.tick_ms"]; ok {
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			c.Tick = time.Duration(i) * time.Millisecond
		}
	}
	if s, ok := v["storage.data_dir"]; ok {
		c.DataDir = s
	}
	if s, ok := v["storage.storage"]; ok {
		c.Storage = s
	}
	if s, ok := v["storage.sqlite_path"]; ok {
		c.SQLitePath = s
	}
	if n, ok := v["storage.retention_hours"]; ok {
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			c.Retention = time.Duration(i) * time.Hour
		}
	}
	if s, ok := v["api.listen"]; ok {
		c.Listen = s
	}
	if s, ok := v["api.token"]; ok {
		c.APIToken = s
	}
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}
