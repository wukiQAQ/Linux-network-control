package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleStream 提供实时推送通道（Server-Sent Events）：
//
//	event: snapshot —— 每秒一条实时指标（与 /api/v1/traffic/now 同结构）
//	event: alert    —— 告警触发/恢复时推送一条
//
// 网页端可直接用 EventSource 订阅；桌面端按行解析即可（无需轮询）。
// 鉴权：优先用 Authorization: Bearer；浏览器 EventSource 无法设置请求头，
// 因此额外接受 ?token=<令牌>（仅本接口，便于内嵌仪表盘使用）。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.cfg.APIToken != "" && !streamTokenOK(r, s.cfg.APIToken) {
		httpError(w, http.StatusUnauthorized, "令牌无效")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "当前服务不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, payload any) bool {
		b, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// 首帧立即推送，避免客户端等满一个周期
	if !send("snapshot", s.statsMap()) {
		return
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	firing := map[string]bool{}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			if !send("snapshot", s.statsMap()) {
				return
			}
			if s.alerts == nil {
				continue
			}
			now := map[string]bool{}
			for _, ev := range s.alerts.Firing() {
				now[ev.RuleID] = true
				if !firing[ev.RuleID] {
					send("alert", ev)
				}
			}
			for id := range firing {
				if !now[id] {
					send("alert", map[string]any{"rule_id": id, "status": "resolved"})
				}
			}
			firing = now
		}
	}
}

// streamTokenOK 校验 Bearer 头或 ?token= 查询参数（常量时间比较）。
func streamTokenOK(r *http.Request, token string) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, prefix) {
		got := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
		return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
	}
	q := strings.TrimSpace(r.URL.Query().Get("token"))
	return q != "" && subtle.ConstantTimeCompare([]byte(q), []byte(token)) == 1
}
