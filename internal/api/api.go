// Package api 提供版本化 REST 接口与静态页面托管。
// 所有接口前缀 /api/v1，返回 JSON；页面由内嵌文件系统（go:embed）提供。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// Server 聚合各模块依赖，提供 HTTP 处理器。
type Server struct {
	agg     *aggregator.Agg
	store   storage.Backend
	table   *flow.Table
	cfg     *config.Config
	alerts  *alert.Engine   // 可选告警引擎，nil 表示未启用
	dumper  *capture.Dumper // 可选按需抓包器，nil 表示未启用导出接口
	started time.Time
	ui      fs.FS
}

func New(cfg *config.Config, agg *aggregator.Agg, table *flow.Table, store storage.Backend, ui fs.FS) *Server {
	return &Server{agg: agg, store: store, table: table, cfg: cfg, started: time.Now(), ui: ui}
}

// SetAlerts 注入告警引擎（nil 表示未启用，接口返回空列表）。
func (s *Server) SetAlerts(e *alert.Engine) {
	s.alerts = e
}

// SetDumper 注入按需抓包器（nil 表示不启用 pcap 导出接口）。
func (s *Server) SetDumper(d *capture.Dumper) {
	s.dumper = d
}

// Handler 返回路由。注意：静态页面注册在 "/"，精确 API 路径优先匹配。
// 配置了 api.token 时，/api/ 前缀请求需要 Authorization: Bearer <token>。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/traffic/now", s.handleNow)
	mux.HandleFunc("GET /api/v1/traffic/history", s.handleHistory)
	mux.HandleFunc("GET /api/v1/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)
	mux.HandleFunc("POST /api/v1/capture/dump", s.handleCaptureStart)
	mux.HandleFunc("GET /api/v1/capture/dump", s.handleCaptureStatus)
	mux.HandleFunc("GET /api/v1/capture/dump/file", s.handleCaptureFile)
	mux.Handle("/", http.FileServerFS(s.ui))
	h := http.Handler(logRequests(mux))
	if s.cfg.APIToken != "" {
		h = requireToken(s.cfg.APIToken, h)
	}
	return h
}

// requireToken 对 /api/ 前缀请求校验 Authorization: Bearer <token>；
// 静态页面不鉴权（不含敏感数据），令牌为空时整个中间件被跳过。
// 使用 subtle.ConstantTimeCompare 避免时序侧信道。
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			const prefix = "Bearer "
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, prefix) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "缺少 Bearer 令牌"})
				return
			}
			got := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) statsMap() map[string]any {
	recv := s.agg.TotalPackets()
	drop := s.agg.TotalDrops()
	rate := 0.0
	if recv+drop > 0 {
		rate = float64(drop) / float64(recv+drop)
	}
	last := s.agg.Last()
	return map[string]any{
		"ts":           time.Now().UTC().Format(time.RFC3339),
		"bps":          last.Bps,
		"pps":          last.Pps,
		"conns":        last.Active,
		"new_conns":    last.NewConns,
		"recv_packets": recv,
		"drop_packets": drop,
		"drop_rate":    rate,
		"uptime_s":     int64(time.Since(s.started).Seconds()),
		"flows_active": s.table.Active(),
		"machine_id":   s.cfg.MachineID,
		"source":       s.cfg.Source,
	}
}

func (s *Server) handleNow(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statsMap())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statsMap())
}

// handleHistory 聚合多个时序序列到同一时间轴上。
// 参数：since（秒，默认 3600）、step（秒，默认 60）。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	since := intParam(r, "since", 3600)
	step := intParam(r, "step", 60)
	if step < 1 {
		step = 1
	}
	to := time.Now().UTC()
	from := to.Add(-time.Duration(since) * time.Second)
	names := map[string]string{
		"traffic.bps":      "bps",
		"traffic.pps":      "pps",
		"traffic.conns":    "conns",
		"traffic.newconns": "new_conns",
		"traffic.drops":    "drops",
	}
	type point struct {
		Bps, Pps, Conns, NewConns, Drops float64
		Count                            int
	}
	n := int(to.Sub(from).Seconds())/step + 1
	rows := make([]point, n)
	for series := range names {
		pts, err := s.store.QueryMetrics(series, from, to)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "查询指标失败: "+err.Error())
			return
		}
		for _, p := range pts {
			stepDur := time.Duration(step) * time.Second
			idx := int(p.Ts.Sub(from) / stepDur)
			if idx < 0 || idx >= n {
				continue
			}
			row := &rows[idx]
			row.Count++
			switch names[series] {
			case "bps":
				row.Bps += p.Value
			case "pps":
				row.Pps += p.Value
			case "conns":
				row.Conns += p.Value
			case "new_conns":
				row.NewConns += p.Value
			case "drops":
				row.Drops += p.Value
			}
		}
	}
	type outPoint struct {
		T        int64   `json:"t"`
		Bps      float64 `json:"bps"`
		Pps      float64 `json:"pps"`
		Conns    float64 `json:"conns"`
		NewConns float64 `json:"new_conns"`
		Drops    float64 `json:"drops"`
	}
	out := make([]outPoint, 0, n)
	for i, row := range rows {
		div := float64(max(row.Count, 1))
		out = append(out, outPoint{
			T:        from.Add(time.Duration(i*step) * time.Second).Unix(),
			Bps:      row.Bps / div,
			Pps:      row.Pps / div,
			Conns:    row.Conns / div,
			NewConns: row.NewConns / div,
			Drops:    row.Drops / div,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":   from.Format(time.RFC3339),
		"to":     to.Format(time.RFC3339),
		"step_s": step,
		"points": out,
	})
}

// handleSessions 会话查询：ip 子串过滤、proto、port、page/page_size。
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := storage.SessionFilter{IP: q.Get("ip")}
	switch strings.ToLower(q.Get("proto")) {
	case "tcp":
		f.Proto = 6
	case "udp":
		f.Proto = 17
	case "icmp":
		f.Proto = 1
	}
	if p, err := strconv.Atoi(q.Get("port")); err == nil && p > 0 {
		f.Port = uint16(p)
	}
	page := intParam(r, "page", 1)
	pageSize := intParam(r, "page_size", 10)
	if page < 1 {
		page = 1
	}
	if pageSize > 100 {
		pageSize = 100
	}
	rows, total, err := s.store.QuerySessions(f, pageSize, (page-1)*pageSize)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询会话失败: "+err.Error())
		return
	}
	type item struct {
		Start     string `json:"start"`
		End       string `json:"end"`
		SrcIP     string `json:"src_ip"`
		SrcPort   uint16 `json:"src_port"`
		DstIP     string `json:"dst_ip"`
		DstPort   uint16 `json:"dst_port"`
		Proto     string `json:"proto"`
		Packets   uint64 `json:"packets"`
		Bytes     uint64 `json:"bytes"`
		TCPFlags  uint8  `json:"tcp_flags"`
		Iface     string `json:"iface"`
		MachineID string `json:"machine_id"`
	}
	items := make([]item, 0, len(rows))
	for _, rec := range rows {
		items = append(items, item{
			Start: rec.Start.Format(time.RFC3339), End: rec.End.Format(time.RFC3339),
			SrcIP: rec.SrcIP, SrcPort: rec.SrcPort, DstIP: rec.DstIP, DstPort: rec.DstPort,
			Proto: protoName(rec.Proto), Packets: rec.Packets, Bytes: rec.Bytes,
			TCPFlags: rec.TCPFlags, Iface: rec.Iface, MachineID: rec.MachineID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "page": page, "page_size": pageSize, "items": items,
	})
}

// handleAlerts 查询告警事件：status=firing|resolved|all（默认全部）。
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	status := strings.ToLower(r.URL.Query().Get("status"))
	if status == "all" {
		status = ""
	}
	if status != "" && status != "firing" && status != "resolved" {
		httpError(w, http.StatusBadRequest, "status 仅支持 firing / resolved / all")
		return
	}
	items := []alert.Event{}
	if s.alerts != nil {
		items = s.alerts.List(status)
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

// handleCaptureStart 开始一次按需抓包，导出 N 秒的 pcap（供 Wireshark 分析）。
// 参数：seconds（默认 10，范围 1~60）。同一时刻只允许一次导出。
func (s *Server) handleCaptureStart(w http.ResponseWriter, r *http.Request) {
	if s.dumper == nil {
		httpError(w, http.StatusServiceUnavailable, "抓包导出未启用")
		return
	}
	seconds := intParam(r, "seconds", 10)
	res, err := s.dumper.Start(seconds)
	if err != nil {
		httpError(w, http.StatusConflict, "开始抓包失败: "+err.Error())
		return
	}
	log.Printf("[api] 开始抓包导出 %s（%d 秒）", filepath.Base(res.File), res.Seconds)
	writeJSON(w, http.StatusOK, capturePayload(res, true))
}

// handleCaptureStatus 查询抓包导出状态：active 表示仍在抓，ready 表示可以下载。
func (s *Server) handleCaptureStatus(w http.ResponseWriter, _ *http.Request) {
	if s.dumper == nil {
		httpError(w, http.StatusServiceUnavailable, "抓包导出未启用")
		return
	}
	res, active := s.dumper.Status()
	writeJSON(w, http.StatusOK, capturePayload(res, active))
}

// handleCaptureFile 下载最近一次导出的 pcap 文件，可用 Wireshark 直接打开。
func (s *Server) handleCaptureFile(w http.ResponseWriter, r *http.Request) {
	if s.dumper == nil {
		httpError(w, http.StatusServiceUnavailable, "抓包导出未启用")
		return
	}
	res, active := s.dumper.Status()
	if res.ID == "" || active {
		httpError(w, http.StatusConflict, "当前没有可下载的抓包文件")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(res.File)+`"`)
	http.ServeFile(w, r, res.File)
}

// capturePayload 组装导出状态响应（文件名只给出基名，避免泄露服务器目录结构）。
func capturePayload(res capture.DumpResult, active bool) map[string]any {
	name := ""
	if res.File != "" {
		name = filepath.Base(res.File)
	}
	out := map[string]any{
		"active":  active,
		"ready":   !active && res.ID != "",
		"id":      res.ID,
		"file":    name,
		"seconds": res.Seconds,
		"packets": res.Packets,
		"bytes":   res.Bytes,
	}
	if !res.Started.IsZero() {
		out["started_at"] = res.Started.Format(time.RFC3339)
	}
	if !res.Ended.IsZero() {
		out["ended_at"] = res.Ended.Format(time.RFC3339)
	}
	return out
}

func protoName(p uint8) string {
	switch p {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 1:
		return "icmp"
	default:
		return strconv.Itoa(int(p))
	}
}

func intParam(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// logRequests 记录每个 API 请求（模块 tag: api）。
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("[api] %s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
