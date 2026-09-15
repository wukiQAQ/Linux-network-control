package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 文件通道：把 Linux 上的文件（日志、配置等）列出来并下载到 Windows。
// 安全约束：只允许访问白名单根目录下的普通文件，拒绝 .. 与符号链接逃逸，限制单文件大小。
var fileRoots = []string{"/var/log", "/etc", "/tmp", "/home", "/var/lib/netmon", "/var/lib/mysql", "/opt"}

const maxDownloadBytes = 64 << 20 // 单文件下载上限 64MB

// fileEntry 是目录列表的一项。
type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// resolveUnderRoots 校验路径在白名单内，并返回清理后的绝对路径。
func resolveUnderRoots(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("路径不能为空")
	}
	if strings.Contains(p, "..") {
		return "", errors.New("路径不允许包含 ..")
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return "", errors.New("必须是绝对路径")
	}
	target := filepath.ToSlash(clean)
	for _, root := range fileRoots {
		r := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
		if target == r || strings.HasPrefix(target, r+"/") {
			return clean, nil
		}
	}
	return "", fmt.Errorf("只允许访问白名单目录：%s", strings.Join(fileRoots, "、"))
}

// handleFilesList 列出目录内容（不递归）。
func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = fileRoots[0]
	}
	clean, err := resolveUnderRoots(dir)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(clean)
	if err != nil {
		httpError(w, http.StatusNotFound, "目录不存在或无法访问: "+err.Error())
		return
	}
	if !info.IsDir() {
		httpError(w, http.StatusBadRequest, "该路径不是目录")
		return
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		httpError(w, http.StatusForbidden, "读取目录失败: "+err.Error())
		return
	}
	items := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, fileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(clean, e.Name()),
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	// 目录在前，其余按名称排序
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"path": clean, "parent": parentDir(clean), "roots": fileRoots,
		"total": len(items), "items": items,
	})
}

func parentDir(p string) string {
	parent := filepath.Dir(p)
	if _, err := resolveUnderRoots(parent); err != nil {
		return ""
	}
	return parent
}

// handleFileDownload 下载单个文件（白名单 + 大小上限）。
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	clean, err := resolveUnderRoots(r.URL.Query().Get("path"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(clean)
	if err != nil {
		httpError(w, http.StatusNotFound, "文件不存在: "+err.Error())
		return
	}
	if info.IsDir() {
		httpError(w, http.StatusBadRequest, "该路径是目录，不能下载")
		return
	}
	if info.Size() > maxDownloadBytes {
		httpError(w, http.StatusRequestEntityTooLarge,
			"文件过大（"+strconv.FormatInt(info.Size()>>20, 10)+" MB），上限 64 MB")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(clean)+`"`)
	http.ServeFile(w, r, clean)
}
