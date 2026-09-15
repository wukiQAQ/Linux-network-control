// 文件通道（Linux → Windows）的纯逻辑：路径处理、排序与大小格式化。

export function parentPath(p) {
  const s = String(p || "").replace(/\/+$/, "");
  if (!s || s === "/") return "";
  const i = s.lastIndexOf("/");
  return i <= 0 ? "/" : s.slice(0, i);
}

export function joinPath(dir, name) {
  const d = String(dir || "").replace(/\/+$/, "");
  const n = String(name || "").replace(/^\/+/, "");
  if (!d) return "/" + n;
  return d + "/" + n;
}

// 目录在前，其余按名称排序（不区分大小写）
export function sortEntries(list) {
  const arr = Array.isArray(list) ? [...list] : [];
  return arr.sort((a, b) => {
    const ad = a && a.is_dir ? 1 : 0;
    const bd = b && b.is_dir ? 1 : 0;
    if (ad !== bd) return bd - ad;
    return String((a && a.name) || "").toLowerCase().localeCompare(String((b && b.name) || "").toLowerCase());
  });
}

export function fileSizeText(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return "-";
  if (n < 1024) return n + " B";
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
}

export function entryIcon(e) {
  return e && e.is_dir ? "📁" : "📄";
}