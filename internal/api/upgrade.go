package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/updater"
)

// handleUpgradeStatus 查询自升级是否启用与最近一次结果。
func (s *Server) handleUpgradeStatus(w http.ResponseWriter, _ *http.Request) {
	if s.updater == nil {
		httpError(w, http.StatusServiceUnavailable, "自升级未启用")
		return
	}
	writeJSON(w, http.StatusOK, s.updater.Status())
}

// handleUpgrade 执行安全版自升级。
// body: {"url":"https://.../netmon-linux","sha256":"<64位十六进制>","confirm":true,"restart":true}
// 安全约束由 internal/updater 强制：白名单 URL + SHA256 + ELF 校验 + 原子替换 + 备份。
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		httpError(w, http.StatusServiceUnavailable, "自升级未启用")
		return
	}
	var req struct {
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Confirm bool   `json:"confirm"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "请求体格式错误: "+err.Error())
		return
	}
	if !req.Confirm {
		httpError(w, http.StatusConflict, "自升级会替换正在运行的程序，需要 confirm=true 二次确认")
		return
	}
	self, err := os.Executable()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "无法确定当前程序路径: "+err.Error())
		return
	}
	sha, err := s.updater.Upgrade(r.Context(), req.URL, req.SHA256, self)
	if err != nil {
		code := http.StatusBadRequest
		if err == updater.ErrDisabled {
			code = http.StatusServiceUnavailable
		}
		httpError(w, code, err.Error())
		return
	}
	if req.Restart {
		// 先返回响应，再重启进程（否则客户端收不到结果）
		s.updater.RestartAfter(time.Second)
	}
	log.Printf("[api] 自升级完成 sha256=%s restart=%v", sha, req.Restart)
	writeJSON(w, http.StatusOK, map[string]any{
		"applied":           true,
		"sha256":            sha,
		"restart_scheduled": req.Restart,
		"note":              "程序已原子替换；若已安排重启，约 1 秒后服务会重启，客户端会自动重连",
	})
}
