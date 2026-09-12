// 抓包导出（Wireshark 联动）相关的纯逻辑与文案。

export const CAPTURE_SECONDS_DEFAULT = 15;
export const CAPTURE_SECONDS_MIN = 1;
export const CAPTURE_SECONDS_MAX = 60;

// 把秒数夹到后端允许的范围（后端同样会再夹一次）。
export function clampCaptureSeconds(v) {
  const n = Math.floor(Number(v) || 0);
  if (!Number.isFinite(n) || n <= 0) return CAPTURE_SECONDS_DEFAULT;
  return Math.min(CAPTURE_SECONDS_MAX, Math.max(CAPTURE_SECONDS_MIN, n));
}

// 抓包中的进度文案：显示已抓包数与已用时间。
export function captureProgressText(status, seconds) {
  const s = status && typeof status === "object" ? status : {};
  const total = clampCaptureSeconds(seconds);
  const packets = Number(s.packets) || 0;
  const bytes = Number(s.bytes) || 0;
  const used = s.started_at ? Math.max(0, Math.round((Date.now() - Date.parse(s.started_at)) / 1000)) : 0;
  const left = Math.max(0, total - (Number.isFinite(used) ? used : 0));
  return `抓包中：已捕获 ${packets} 包 / ${bytes} 字节，剩余约 ${left} 秒`;
}

// 完成后的文案。
export function captureDoneText(status) {
  const s = status && typeof status === "object" ? status : {};
  const packets = Number(s.packets) || 0;
  const bytes = Number(s.bytes) || 0;
  return `抓包完成：${packets} 包 / ${bytes} 字节`;
}

// 抓包文件名（后端只返回基名）。
export function captureFileName(status) {
  const s = status && typeof status === "object" ? status : {};
  return typeof s.file === "string" ? s.file : "";
}