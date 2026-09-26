package api

import (
	"net/http"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/storage"
	"github.com/wukiQAQ/Linux-network-control/internal/topn"
)

// handleTopN 返回最近一段时间内的 TOP IP / 协议分布 / TOP 端口。
// 参数：since（秒，默认 3600）、n（默认 10，上限 100）。
// 说明：聚合在存储层完成（SQLite 走 SQL GROUP BY），因此**不再有 2 万条样本上限**。
func (s *Server) handleTopN(w http.ResponseWriter, r *http.Request) {
	since := intParam(r, "since", 3600)
	if since <= 0 {
		since = 3600
	}
	n := intParam(r, "n", 10)
	if n <= 0 || n > 100 {
		n = 10
	}
	from := time.Now().UTC().Add(-time.Duration(since) * time.Second)
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
