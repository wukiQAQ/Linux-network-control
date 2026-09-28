// 文件通道（Linux → Windows）的纯逻辑：路径处理、排序与大小格式化。
import { fmtBytes } from "./format.js";

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

// 字节格式化统一走 format.js，避免同一套逻辑在多个模块各写一份。
export function fileSizeText(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return "-";
  return fmtBytes(n);
}

export function entryIcon(e) {
  return e && e.is_dir ? "📁" : "📄";
}

// 可执行 / 脚本类扩展名：Rust 侧 open_local_file 会拒绝这些，
// 前端在调用前也先拦一道，并把「拒绝原因」与「二次确认文案」统一在这里定义。
// 名单与 Rust 侧 client/src-tauri/src/lib.rs 的 BLOCKED_EXTS 逐项保持一致
// （改一处就要改另一处；本文件的测试在 client/test/files.test.mjs）。
const DANGEROUS_EXTS = [
  "exe", "bat", "cmd", "com", "scr", "pif", "msi", "msp", "cpl",
  "ps1", "psm1", "vbs", "vbe", "js", "jse", "wsf", "wsh", "hta",
  "lnk", "url", "reg", "jar", "chm", "inf",
];

// 取小写扩展名（不含点）；无扩展名返回空串。
// 注意按「最后一个点」取，因此 report.pdf.exe 得到 exe —— 正是要拦的目标。
export function fileExt(name) {
  const s = String(name || "").trim().toLowerCase();
  const base = s.split(/[\\/]/).pop() || "";
  const i = base.lastIndexOf(".");
  if (i <= 0 || i === base.length - 1) return "";
  return base.slice(i + 1);
}

// 该文件名是否属于「会执行代码」的类型。
export function isDangerousExt(name) {
  return DANGEROUS_EXTS.includes(fileExt(name));
}

// 下载后是否允许用系统默认程序打开：可执行类一律不自动/不直接打开。
export function canOpenLocalFile(name) {
  return !isDangerousExt(name);
}
