// 前端统一通过 Rust 命令访问远程 API（避免 CORS，令牌保存在 Rust 状态中）
import { invoke } from "@tauri-apps/api/core";

export function ping() {
  return invoke("ping");
}

export function setConnection(base, token) {
  return invoke("set_connection", { base: base, token: token || "" });
}

export function clearConnection() {
  return invoke("clear_connection");
}

export function apiGet(path) {
  return invoke("api_get", { path: path });
}

// POST（无请求体），用于开始抓包等动作类接口
export function apiPost(path) {
  return invoke("api_post", { path: path });
}

// 把服务端的抓包文件下载到本机临时目录，返回保存路径
export function saveCapture(path) {
  return invoke("save_capture", { path: path });
}

// 用 Wireshark 打开本地 pcap 文件
export function openInWireshark(path) {
  return invoke("open_in_wireshark", { path: path });
}

// 提取 Rust 命令返回的错误信息
export function errText(e) {
  if (typeof e === "string") return e;
  if (e && typeof e.message === "string") return e.message;
  return String(e || "未知错误");
}
// 开机自启动（tauri-plugin-autostart）
export function autostartEnable() {
  return invoke("plugin:autostart|enable");
}
export function autostartDisable() {
  return invoke("plugin:autostart|disable");
}
export function autostartIsEnabled() {
  return invoke("plugin:autostart|is_enabled");
}