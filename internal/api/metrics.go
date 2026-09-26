package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/buildinfo"
)

// metricsSnapshot 是渲染 /metrics 需要的全部数据（纯结构，便于单测）。
type metricsSnapshot struct {
	Machine  string
	Version  string
	Features []string

	Bps      float64
	Pps      float64
	Conns    int
	NewConns int
	Closed   int
	Recv     uint64
	Drop     uint64
	DropRate float64
	UptimeS  int64
	Flows    int

	// 采集过滤：FilterOn=false 时不输出过滤相关指标
	FilterOn      bool
	FilterExpr    string
	FilterKernel  bool
	FilterDropped uint64
}

// handleMetrics 输出 Prometheus 文本格式指标，便于接入现有监控体系（Prometheus/Grafana 等）。
// 注意：与 /api/ 一样受 [api] token 保护（Prometheus 侧用 bearer_token_file 配置即可）。
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	last := s.agg.Last()
	snap := metricsSnapshot{
		Machine:  s.cfg.MachineID,
		Version:  buildinfo.Version,
		Features: buildinfo.Features,
		Bps:      last.Bps,
		Pps:      last.Pps,
		Conns:    last.Active,
		NewConns: last.NewConns,
		Closed:   last.Closed,
		Recv:     s.agg.TotalPackets(),
		Drop:     s.agg.TotalDrops(),
		UptimeS:  int64(time.Since(s.started).Seconds()),
		Flows:    s.table.Active(),
	}
	if snap.Recv+snap.Drop > 0 {
		snap.DropRate = float64(snap.Drop) / float64(snap.Recv+snap.Drop)
	}
	if s.filter != "" {
		snap.FilterOn = true
		snap.FilterExpr = s.filter
		snap.FilterKernel = s.filterKernel
		if s.filteredFn != nil {
			snap.FilterDropped = s.filteredFn()
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, renderMetrics(snap))
}

// renderMetrics 把快照渲染成 Prometheus 文本格式。
// 指标命名统一 netmon_ 前缀，所有样本都带 machine 标签，便于多台机器在同一个 Prometheus 里区分与聚合。
func renderMetrics(m metricsSnapshot) string {
	var b strings.Builder
	base := `machine="` + escapeLabelValue(m.Machine) + `"`

	head := func(name, help, kind string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	}
	sample := func(name, labels string, value string) {
		fmt.Fprintf(&b, "%s{%s} %s\n", name, labels, value)
	}
	labels := func(extra string) string {
		if extra == "" {
			return base
		}
		return base + "," + extra
	}
	gauge := func(name, help string, v float64, extra string) {
		head(name, help, "gauge")
		sample(name, labels(extra), formatFloat(v))
	}
	counter := func(name, help string, v uint64, extra string) {
		head(name, help, "counter")
		sample(name, labels(extra), strconv.FormatUint(v, 10))
	}

	head("netmon_build_info", "构建信息，恒为 1，版本与机器名在标签里", "gauge")
	sample("netmon_build_info", labels(`version="`+escapeLabelValue(m.Version)+`"`), "1")

	if len(m.Features) > 0 {
		head("netmon_feature_enabled", "服务端具备的能力（1 = 支持）", "gauge")
		for _, f := range m.Features {
			sample("netmon_feature_enabled", labels(`feature="`+escapeLabelValue(f)+`"`), "1")
		}
	}

	gauge("netmon_traffic_bps", "最近一个聚合周期的带宽（bit/s）", m.Bps, "")
	gauge("netmon_traffic_pps", "最近一个聚合周期的包速率（包/s）", m.Pps, "")
	gauge("netmon_conns_active", "当前活跃会话数", float64(m.Conns), "")
	gauge("netmon_conns_new", "最近一个聚合周期新建的 TCP 连接数", float64(m.NewConns), "")
	gauge("netmon_conns_closed", "最近一个聚合周期关闭的 TCP 连接数", float64(m.Closed), "")
	gauge("netmon_drop_rate", "抓包丢包率（0~1）", m.DropRate, "")
	gauge("netmon_uptime_seconds", "采集端运行时长（秒）", float64(m.UptimeS), "")
	gauge("netmon_flows_active", "流表中的活跃会话数", float64(m.Flows), "")
	counter("netmon_recv_packets_total", "累计接收报文数", m.Recv, "")
	counter("netmon_drop_packets_total", "累计抓包丢包数（含内核上报）", m.Drop, "")

	if m.FilterOn {
		head("netmon_filter_info", "采集过滤表达式（恒为 1，表达式在标签里）", "gauge")
		sample("netmon_filter_info", labels(`filter="`+escapeLabelValue(m.FilterExpr)+`"`), "1")
		gauge("netmon_filter_kernel", "是否启用内核剪枝（1 = 已启用）", boolValue(m.FilterKernel), "")
		counter("netmon_filter_dropped_total", "被用户态过滤掉的报文数（内核剪枝丢弃的不计入）", m.FilterDropped, "")
	}
	return b.String()
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// formatFloat 输出不带指数、无多余零的数值（Prometheus 文本格式对数值格式要求宽松，这样更易读）。
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// escapeLabelValue 按 Prometheus 文本格式转义标签值（反斜杠、双引号、换行）。
func escapeLabelValue(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}
