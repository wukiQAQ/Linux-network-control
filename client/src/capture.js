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

// 把抓包过程中的错误翻译成可操作的提示，避免只显示 "HTTP 404"。
export function captureErrorText(err) {
  const text = String(err || "");
  if (text.includes("404")) return "服务端版本过旧：没有抓包接口，请把 Linux 端更新到 V0.7.0 及以上";
  if (text.includes("409")) return "已有抓包任务在进行中，请等它结束后重试";
  if (text.includes("503")) return "服务端未启用抓包导出（通常是旧版本）";
  if (/超时|timeout/i.test(text)) return "抓包超时，请检查网络后重试";
  return text || "未知错误";
}