package api

import (
	"net/http"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/topn"
)

// handleTopN 返回最近一段时间内的 TOP IP / 协议分布 / TOP 端口。
// 参数：since（秒，默认 3600，上限 30 天）、n（默认 10，上限 100）。
// 说明：聚合在存储层完成（SQLite 走 SQL GROUP BY），因此**不再有 2 万条样本上限**。
func (s *Server) handleTopN(w http.ResponseWriter, r *http.Request) {
	since := intParam(r, "since", 3600)
	if since <= 0 {
		since = 3600
	}
	// 上界与 /traffic/history 一致：since 极大时 time.Duration(since)*Second 会溢出。
	if since > maxHistorySeconds {
		since = maxHistorySeconds
	}
	n := intParam(r, "n", 10)
	if n <= 0 || n > 100 {
		n = 10
	}
	// 窗口右端取"数据时间"（离线回放时即最后一条数据的时间）：
	// 回放历史 pcap 时数据时间远早于墙钟，按墙钟取窗口会得到空榜。
	from := s.now().Add(-time.Duration(since) * time.Second)
	agg, err := s.store.AggregateFlows(storage.SessionFilter{From: from}, n)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "聚合会话失败: "+err.Error())
		return
	}
	res := topn.FromAggregate(agg)
	writeJSON(w, http.StatusOK, map[string]any{
		"since_s":      since,
		"n":            n,
		"total_flows":  res.TotalFlows,
		"total_bytes":  res.TotalBytes,
		"by_ip":        res.ByIP,
		"by_proto":     res.ByProto,
		"by_port":      res.ByPort,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	})
}
