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
//
// 这里只做**词法**校验（拒绝 ..、必须是绝对路径、必须落在白名单内），
// 但文件系统里的符号链接能让词法上合法的路径指向白名单之外（例如 /tmp/x -> /etc），
// 因此调用方拿到结果后必须再走 resolveSymlinks 把链接解析掉并重新校验。
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
	for _, root := range fileRoots {
		if isUnderRoot(clean, root) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("只允许访问白名单目录：%s", strings.Join(fileRoots, "、"))
}

// isUnderRoot 判断绝对路径 p 是否落在根目录 root 之内（用文件系统语义比较，不做字符串前缀）。
// 跨平台注意：不能用 strings.TrimPrefix(p, "/") 之类的方式做前缀匹配——
// Windows 上绝对路径是 `C:\...`，没有前导斜杠。
func isUnderRoot(p, root string) bool {
	r := filepath.Clean(root)
	c := filepath.Clean(p)
	if c == r {
		return true
	}
	rel, err := filepath.Rel(r, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// maxSymlinkDepth 限制单个路径里解析符号链接的跳数，避免链接成环时死循环。
const maxSymlinkDepth = 40

// resolveSymlinks 逐段解析 p 中的符号链接，返回最终的真实路径。
//
// 中间段（末端之前）的链接必须解析，否则逃逸就发生在中间段；
// 末端如果是链接则直接拒绝：以 /etc/mtab 为例它指向 /proc/self/mounts，
// 即使把内容读出来也不算访问白名单内的文件，直接拒绝最简单也最安全。
// 不存在的路径（如列出尚未创建的目录）原样返回，让调用方照旧报 404。
func resolveSymlinks(p string) (string, error) {
	cur := filepath.Clean(p)
	for depth := 0; depth < maxSymlinkDepth; depth++ {
		suffix, err := firstSymlink(cur)
		if err != nil {
			return "", err
		}
		if suffix == "" {
			return cur, nil
		}
		link := filepath.Join(cur, suffix)
		target, err := os.Readlink(link)
		if err != nil {
			if os.IsNotExist(err) {
				return cur, nil
			}
			return "", err
		}
		target = filepath.Clean(target)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(link), target)
		}
		cur = filepath.Clean(target)
	}
	return "", errors.New("路径中的符号链接层级过深")
}

// firstSymlink 返回从 cur 起第一个符号链接组件的相对路径（形如 "a/b"）。
// 没有任何符号链接组件、或某段不存在时返回 ""（不存在的路径交给调用方按 404 处理）。
func firstSymlink(cur string) (string, error) {
	clean := filepath.Clean(cur)
	// VolumeName 在 Unix 上是空的，因此根是 "/"；在 Windows 上是 "C:"，根是 `C:\`。
	root := filepath.VolumeName(clean) + string(filepath.Separator)
	rel := strings.TrimPrefix(clean, root)
	parts := strings.Split(rel, string(filepath.Separator))
	acc := root
	var visited []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		acc = filepath.Join(acc, part)
		visited = append(visited, part)
		info, err := os.Lstat(acc)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return filepath.Join(visited...), nil
		}
	}
	return "", nil
}

// securePath 把路径解析成真实路径并确认它仍在白名单根目录内，返回可安全使用的绝对路径。
// 词法校验 -> 解析符号链接 -> 按真实路径重新做前缀比较，并拒绝末端是符号链接的路径。
func securePath(p string) (string, error) {
	clean, err := resolveUnderRoots(p)
	if err != nil {
		return "", err
	}
	real, err := resolveSymlinks(clean)
	if err != nil {
		return "", err
	}
	if fi, err := os.Lstat(real); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("不允许访问符号链接：%s", p)
	}
	for _, root := range fileRoots {
		if isUnderRoot(real, root) {
			return real, nil
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
	clean, err := securePath(dir)
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
	if _, err := securePath(parent); err != nil {
		return ""
	}
	return parent
}

// handleFileDownload 下载单个文件（白名单 + 解析符号链接 + 大小上限）。
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	clean, err := securePath(r.URL.Query().Get("path"))
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
