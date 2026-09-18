// TOP N / 协议分布视图的纯逻辑。

export const TOPN_RANGES = [
  { value: 600, label: "近 10 分钟" },
  { value: 3600, label: "近 1 小时" },
];

// 进度条宽度：最小 2% 便于看清，最大 100%
export function barWidth(percent) {
  const p = Number(percent);
  if (!Number.isFinite(p) || p <= 0) return "2%";
  return Math.min(100, Math.max(2, p)).toFixed(1) + "%";
}

// 字节数的简短格式化
export function shortBytes(bytes) {
  const n = Number(bytes) || 0;
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

// 一行摘要：字节 · 会话数 · 占比
export function entrySummary(e) {
  const bytes = Number(e && e.bytes) || 0;
  const flows = Number(e && e.flows) || 0;
  const percent = Number(e && e.percent) || 0;
  return shortBytes(bytes) + " · " + flows + " 会话 · " + percent.toFixed(1) + "%";
}

// 端口/协议的显示名
export function entryLabel(kind, key) {
  if (kind === "port") return ":" + key;
  return String(key || "-");
}

// 视图标题
export function panelTitle(res) {
  const r = res && typeof res === "object" ? res : {};
  const flows = Number(r.total_flows) || 0;
  const bytes = Number(r.total_bytes) || 0;
  return "会话 " + flows + " 条 · 合计 " + shortBytes(bytes);
}