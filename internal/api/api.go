// Package api 提供版本化 REST 接口与静态页面托管。
// 所有接口前缀 /api/v1，返回 JSON；页面由内嵌文件系统（go:embed）提供。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/action"
	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/alert"
	"github.com/wukiQAQ/Linux-network-control/internal/buildinfo"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/plugin"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/updater"
)

// AuthWarning 判断当前配置是否处于「未配置令牌、且监听地址可能被同网段访问」的状态。
// 返回空串表示无需提醒。
//
// 背景（《代码审查报告》H2）：`[api] token` 为空时整套鉴权中间件会被跳过，
// 而默认监听是 `:8080`（所有网卡）。这里刻意**只做提醒、不改默认值**：
// 改默认监听会让现有部署与文档全部失效，所以留给使用者自己决定。
func AuthWarning(token, listen string) string {
	if token != "" || listenIsLoopback(listen) {
		return ""
	}
	return fmt.Sprintf("未配置 [api] token，且监听地址为 %s（非回环）。"+
		"此时所有管理接口都无需认证：同网段的任何机器都能触发运维动作、下载白名单目录文件、触发自升级、读取实时流量。"+
		"若这不是有意为之，请在配置里设置 [api] token，或把 api.listen 改为 127.0.0.1:8080（只允许本机访问）", listen)
}

// listenIsLoopback 判断监听地址是否只对本机可见。
// 无法确定时返回 false（宁可多提醒一次，也不漏掉真正暴露的配置）。
func listenIsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		// 没有端口（例如 `localhost`）时按整串当主机名；空串一律按非回环处理。
		host = strings.TrimSpace(listen)
		if host == "" {
			return false
		}
	}
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]")
	switch strings.ToLower(host) {
	case "":
		// `:8080`、`[::]:8080` —— 监听所有网卡
		return false
	case "localhost":
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// 既不是 IP 也不是 localhost（例如某个主机名）：解析一次，任一结果落在回环段才算回环。
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			return false
		}
		for _, a := range addrs {
			if p := net.ParseIP(a); p == nil || !p.IsLoopback() {
				return false
			}
		}
		return true
	}
	return ip.IsLoopback()
}

// Server 聚合各模块依赖，提供 HTTP 处理器。
type Server struct {
	agg     *aggregator.Agg
	store   storage.Backend
	table   *flow.Table
	cfg     *config.Config
	alerts  *alert.Engine    // 可选告警引擎，nil 表示未启用
	dumper  *capture.Dumper  // 可选按需抓包器，nil 表示未启用导出接口
	updater *updater.Updater // 可选自升级器，nil 表示未启用升级接口
	actions *action.Executor // 可选运维动作执行器，nil 表示未启用
	// 采集过滤（capture.filter）：表达式与已过滤帧数，
	// 上报给客户端/网页，避免用户看到"流量不全"时误判成采集故障。
	filter       string
	filteredFn   func() uint64
	filterKernel bool
	// 界面插件框架（服务端部分）：已加载的声明式插件清单与加载警告
	plugins        []plugin.Spec
	pluginWarnings []string
	pluginsEnabled bool
	started        time.Time
	ui             fs.FS
	// nowFn 提供查询窗口使用的"数据时间"基准，nil 表示用墙钟。
	// 离线回放时由 cmd/netmon 注入 app.DataNow，详见 SetDataClock。
	nowFn func() time.Time
}

// maxHistorySeconds 限制 /traffic/history 的回溯窗口（30 天）。
// 时间轴按 (since/step + 1) 预分配，必须有界，否则单个请求就能把内存打满。
const maxHistorySeconds = 30 * 24 * 3600

// maxPageSize 限制 /sessions 的单页条数。
const maxPageSize = 100

func New(cfg *config.Config, agg *aggregator.Agg, table *flow.Table, store storage.Backend, ui fs.FS) *Server {
	return &Server{agg: agg, store: store, table: table, cfg: cfg, started: time.Now(), ui: ui}
}

// SetDataClock 注入"数据时间"时钟（例如 app.DataNow）。
//
// 为什么需要：离线回放（source=replay + core.offline）的数据时间来自 pcap 报文
// 时间戳，可能远早于墙钟。此时若仍按 time.Now() 取"最近 N 秒"窗口，回放数据会
// 整体落在窗口之外——/traffic/history 与 /topn 返回空结果，而数据其实已经落盘。
// 不注入时一律用墙钟，行为与历史版本完全一致。
func (s *Server) SetDataClock(f func() time.Time) { s.nowFn = f }

// now 返回查询窗口应使用的时间基准（UTC）：注入了数据时钟且其值非零时用它，
// 否则退回墙钟。
func (s *Server) now() time.Time {
	if s.nowFn != nil {
		if t := s.nowFn(); !t.IsZero() {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// SetAlerts 注入告警引擎（nil 表示未启用，接口返回空列表）。
func (s *Server) SetAlerts(e *alert.Engine) {
	s.alerts = e
}

// SetDumper 注入按需抓包器（nil 表示不启用 pcap 导出接口）。
func (s *Server) SetDumper(d *capture.Dumper) {
	s.dumper = d
}

// SetUpdater 注入自升级器（nil 表示不启用升级接口）。
func (s *Server) SetUpdater(u *updater.Updater) {
	s.updater = u
}

// SetActions 注入运维动作执行器（nil 表示不启用动作接口）。
func (s *Server) SetActions(e *action.Executor) {
	s.actions = e
}

// SetFilter 注入采集过滤信息（表达式 + 已过滤帧数计数器 + 是否启用了内核剪枝）。
func (s *Server) SetFilter(expr string, dropped func() uint64, kernel bool) {
	s.filter = expr
	s.filteredFn = dropped
	s.filterKernel = kernel
}

// SetPlugins 注入界面插件清单与加载警告；enabled 表示配置里指定了插件目录。
// 注入时复制并排序一次，保证接口返回顺序稳定，且不修改调用方的切片。
func (s *Server) SetPlugins(specs []plugin.Spec, warnings []string, enabled bool) {
	sorted := make([]plugin.Spec, len(specs))
	copy(sorted, specs)
	plugin.Sort(sorted)
	s.plugins = sorted
	s.pluginWarnings = warnings
	s.pluginsEnabled = enabled
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
	mux.HandleFunc("GET /api/v1/actions", s.handleActions)
	mux.HandleFunc("POST /api/v1/actions/run", s.handleActionRun)
	mux.HandleFunc("GET /api/v1/actions/history", s.handleActionHistory)
	mux.HandleFunc("GET /api/v1/files", s.handleFilesList)
	mux.HandleFunc("GET /api/v1/files/download", s.handleFileDownload)
	mux.HandleFunc("GET /api/v1/stream", s.handleStream)
	mux.HandleFunc("GET /api/v1/topn", s.handleTopN)
	mux.HandleFunc("GET /api/v1/plugins", s.handlePlugins)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/v1/system/upgrade", s.handleUpgradeStatus)
	mux.HandleFunc("POST /api/v1/system/upgrade", s.handleUpgrade)
	mux.Handle("/", http.FileServerFS(s.ui))
	h := http.Handler(logRequests(mux))
	if s.cfg.APIToken != "" {
		h = requireToken(s.cfg.APIToken, h)
	}
	return h
}

// requireToken 对 /api/ 前缀请求与 /metrics 校验 Authorization: Bearer <token>；
// 静态页面不鉴权（不含敏感数据），令牌为空时整个中间件被跳过。
// /metrics 走同一套令牌（Prometheus 侧用 bearer_token_file 配置即可抓取）。
// 使用 subtle.ConstantTimeCompare 避免时序侧信道。
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/stream 自行校验令牌（浏览器 EventSource 不能设置请求头）
		needAuth := r.URL.Path == "/metrics" ||
			(strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/stream")
		if needAuth {
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
	m := map[string]any{
		// ts 是"当前数据时间"（见 Server.now）：离线回放时是最后一个报文的数据时间，
		// 在线模式与墙钟一致；客户端据此区分"数据本身的时间"与服务器时间。
		"ts":           s.now().Format(time.RFC3339),
		"os":           runtime.GOOS,
		"bps":          last.Bps,
		"pps":          last.Pps,
		"conns":        last.Active,
		"new_conns":    last.NewConns,
		"closed_conns": last.Closed,
		"recv_packets": recv,
		"drop_packets": drop,
		"drop_rate":    rate,
		"uptime_s":     int64(time.Since(s.started).Seconds()),
		"flows_active": s.table.Active(),
		"machine_id":   s.cfg.MachineID,
		"source":       s.cfg.Source,
		"version":      buildinfo.Version,
		"features":     buildinfo.Features,
	}
	// 采集过滤：把表达式与已过滤帧数一并上报（仅在启用过滤时出现）
	if s.filter != "" {
		m["filter"] = s.filter
		m["filter_kernel"] = s.filterKernel
		if s.filteredFn != nil {
			m["filter_dropped"] = s.filteredFn()
		}
	}
	return m
}

func (s *Server) handleNow(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statsMap())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statsMap())
}

// handleHistory 聚合多个时序序列到同一时间轴上。
// 参数：since（秒，默认 3600，上限 30 天）、step（秒，默认 60，范围 1~since）。
//
// since/step 都必须有界：下面要按 (since/step + 1) 预分配 rows，
// 不设上限时 ?since=2147483647&step=1 会让单个未鉴权请求直接申请几十 GB 内存。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	since := intParam(r, "since", 3600)
	if since <= 0 {
		since = 3600
	}
	if since > maxHistorySeconds {
		since = maxHistorySeconds
	}
	step := intParam(r, "step", 60)
	if step < 1 {
		step = 1
	}
	// step 必须被 since 约束：step > since 时时间轴只有一个桶，
	// 而下面 i*step 会被当作纳秒偏移算出远在 from 之外的 t。
	if step > since {
		step = since
	}
	// 窗口右端用"数据时间"（离线回放时即最后一条数据的时间），
	// 否则回放历史 pcap 时 from/to 都在数据之后，曲线恒为空。
	to := s.now()
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
	// port 必须落在 uint16 范围内：原先只判 p > 0，
	// ?port=70000 会被静默截断成 4464，用户以为查的是 70000 却拿到别的端口的数据。
	if p, err := strconv.Atoi(q.Get("port")); err == nil && p > 0 && p <= 65535 {
		f.Port = uint16(p)
	}
	page := intParam(r, "page", 1)
	pageSize := intParam(r, "page_size", 10)
	if page < 1 {
		page = 1
	}
	// page_size 有上界也要有下界：0 或负数会原样传给存储层当作 LIMIT。
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
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

// handleActions 返回白名单动作目录（含每个动作将要执行的命令，供客户端预览）。
func (s *Server) handleActions(w http.ResponseWriter, _ *http.Request) {
	if s.actions == nil {
		httpError(w, http.StatusServiceUnavailable, "运维动作未启用")
		return
	}
	items := action.Catalog()
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

// handleActionRun 执行一个白名单动作。
// 请求体：{"id":"system.disk","params":{},"confirm":false}
func (s *Server) handleActionRun(w http.ResponseWriter, r *http.Request) {
	if s.actions == nil {
		httpError(w, http.StatusServiceUnavailable, "运维动作未启用")
		return
	}
	var req struct {
		ID      string            `json:"id"`
		Params  map[string]string `json:"params"`
		Confirm bool              `json:"confirm"`
	}
	body := http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "请求体格式错误: "+err.Error())
		return
	}
	res, err := s.actions.Run(r.Context(), req.ID, req.Params, req.Confirm)
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, action.ErrUnknownAction):
			code = http.StatusNotFound
		case errors.Is(err, action.ErrConfirmRequired):
			code = http.StatusConflict
		}
		httpError(w, code, err.Error())
		return
	}
	log.Printf("[api] 执行动作 %s exit=%d 用时=%dms", res.ID, res.ExitCode, res.DurationMS)
	writeJSON(w, http.StatusOK, res)
}

// handleActionHistory 返回最近的执行审计记录（新的在前）。
func (s *Server) handleActionHistory(w http.ResponseWriter, _ *http.Request) {
	if s.actions == nil {
		httpError(w, http.StatusServiceUnavailable, "运维动作未启用")
		return
	}
	items := s.actions.History()
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

// protoName 把协议号转成小写名称（客户端用它作为 CSS 类名 .proto.tcp），
// 因此不能与 topn.ProtoName（大写展示名）合并。
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
