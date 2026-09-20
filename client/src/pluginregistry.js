// 界面插件框架（客户端侧纯逻辑）：校验服务端下发的插件清单、按服务端能力门控、
// 维护启用/禁用状态，并把 widget 定义解析成可渲染的数据。
// 服务端已经校验过一次，这里是第二道防线——不信任任何外部输入。

import { fmtBytes, fmtNum, fmtRate } from "./format.js";

export const PLUGIN_DISABLED_KEY = "metd.plugins.disabled.v1";
export const MAX_WIDGETS = 20;
export const MAX_FIELDS = 12;
export const MAX_COLUMNS = 12;

const ID_RE = /^[a-z][a-z0-9._-]{1,31}$/;
const WIDGET_TYPES = ["kpi", "table", "text"];
const UNITS = ["", "bytes", "rate", "count", "percent", "ratio"];
const API_PREFIX = "/api/v1/";

// 插件只能读本机监控接口：拒绝绝对地址、协议相对地址、反斜杠与空白字符。
export function safeEndpoint(ep) {
  if (typeof ep !== "string" || !ep.startsWith(API_PREFIX)) return false;
  if (/[\s\\]/.test(ep)) return false;
  if (ep.startsWith("/api/v1//")) return false;
  return true;
}

function validateWidget(w) {
  if (!w || typeof w !== "object") return { ok: false, reason: "不是对象" };
  const type = typeof w.type === "string" ? w.type.toLowerCase() : "";
  if (!WIDGET_TYPES.includes(type)) return { ok: false, reason: "type 不支持：" + type };
  if (type === "text") {
    return typeof w.text === "string" && w.text.trim() ? { ok: true, reason: "" } : { ok: false, reason: "text 为空" };
  }
  if (w.endpoint && !safeEndpoint(w.endpoint)) return { ok: false, reason: "endpoint 越界" };
  if (type === "kpi") {
    if (!Array.isArray(w.fields) || !w.fields.length) return { ok: false, reason: "kpi 缺 fields" };
    if (w.fields.length > MAX_FIELDS) return { ok: false, reason: "fields 过多" };
    for (const f of w.fields) {
      if (!f || typeof f.key !== "string" || !f.key) return { ok: false, reason: "field 缺 key" };
      if (f.unit && !UNITS.includes(String(f.unit).toLowerCase())) return { ok: false, reason: "unit 不支持" };
    }
    return { ok: true, reason: "" };
  }
  if (typeof w.endpoint !== "string" || !w.endpoint || !safeEndpoint(w.endpoint)) {
    return { ok: false, reason: "table 缺合法 endpoint" };
  }
  if (!Array.isArray(w.columns) || !w.columns.length) return { ok: false, reason: "table 缺 columns" };
  if (w.columns.length > MAX_COLUMNS) return { ok: false, reason: "columns 过多" };
  for (const c of w.columns) {
    if (!c || typeof c.key !== "string" || !c.key) return { ok: false, reason: "column 缺 key" };
    if (c.unit && !UNITS.includes(String(c.unit).toLowerCase())) return { ok: false, reason: "unit 不支持" };
  }
  return { ok: true, reason: "" };
}

export function validatePlugin(spec) {
  if (!spec || typeof spec !== "object") return { ok: false, reason: "不是对象" };
  if (typeof spec.id !== "string" || !ID_RE.test(spec.id)) return { ok: false, reason: "id 不合法" };
  if (typeof spec.title !== "string" || !spec.title.trim()) return { ok: false, reason: "缺少 title" };
  if (!Array.isArray(spec.widgets) || !spec.widgets.length) return { ok: false, reason: "缺少 widgets" };
  if (spec.widgets.length > MAX_WIDGETS) return { ok: false, reason: "widgets 过多" };
  for (const w of spec.widgets) {
    const r = validateWidget(w);
    if (!r.ok) return { ok: false, reason: "widgets：" + r.reason };
  }
  return { ok: true, reason: "" };
}

function normalizeWidget(w) {
  return {
    type: String(w.type).toLowerCase(),
    title: typeof w.title === "string" ? w.title.trim() : "",
    text: typeof w.text === "string" ? w.text : "",
    endpoint: typeof w.endpoint === "string" ? w.endpoint : "",
    params: w.params && typeof w.params === "object" ? { ...w.params } : {},
    root: typeof w.root === "string" && w.root ? w.root : "items",
    fields: Array.isArray(w.fields) ? w.fields.map((f) => ({ key: f.key, label: f.label || f.key, unit: String(f.unit || "").toLowerCase() })) : [],
    columns: Array.isArray(w.columns)
      ? w.columns.map((c) => ({ key: c.key, label: c.label || c.key, unit: String(c.unit || "").toLowerCase() }))
      : [],
  };
}

export function normalizePlugin(spec) {
  return {
    id: spec.id,
    title: spec.title.trim(),
    icon: typeof spec.icon === "string" && spec.icon.trim() ? spec.icon.trim() : "🧩",
    order: Number.isFinite(Number(spec.order)) ? Number(spec.order) : 0,
    refresh_s: Math.min(600, Math.max(0, Number(spec.refresh_s) || 0)),
    requires: Array.isArray(spec.requires)
      ? [...new Set(spec.requires.filter((r) => typeof r === "string" && r.trim()).map((r) => r.trim()))]
      : [],
    widgets: (spec.widgets || []).map(normalizeWidget),
  };
}

// 校验 → 去重（同 id 只保留第一个）→ 按 order / 标题排序。
export function normalizePlugins(list) {
  const out = [];
  const seen = new Set();
  for (const raw of Array.isArray(list) ? list : []) {
    if (!validatePlugin(raw).ok) continue;
    const spec = normalizePlugin(raw);
    if (seen.has(spec.id)) continue;
    seen.add(spec.id);
    out.push(spec);
  }
  return out.sort((a, b) => a.order - b.order || a.title.localeCompare(b.title, "zh-CN") || a.id.localeCompare(b.id));
}

// 服务端能力门控：缺少的能力列在 missing 里，hint 可直接展示给用户。
export function pluginAvailability(spec, features) {
  const have = Array.isArray(features) ? features : [];
  const need = spec && Array.isArray(spec.requires) ? spec.requires : [];
  const missing = need.filter((r) => !have.includes(r));
  return {
    ok: missing.length === 0,
    missing,
    hint: missing.length ? "需要服务端能力：" + missing.join("、") + "（请升级 Linux 端 netmon）" : "",
  };
}

// 合并成界面用的插件列表：带启用状态与可用性。
export function pluginList(list, { features = [], disabled = [] } = {}) {
  const off = Array.isArray(disabled) ? disabled : [];
  return normalizePlugins(list).map((spec) => {
    const a = pluginAvailability(spec, features);
    return { ...spec, enabled: !off.includes(spec.id), available: a.ok, missing: a.missing, hint: a.hint };
  });
}

export function navKey(id) {
  return "plugin:" + id;
}

export function pluginIdFromNavKey(key) {
  const s = typeof key === "string" ? key : "";
  return s.startsWith("plugin:") ? s.slice(7) : "";
}

export function togglePluginDisabled(disabledIds, id, enabled) {
  const set = new Set(Array.isArray(disabledIds) ? disabledIds : []);
  if (enabled) set.delete(id);
  else set.add(id);
  return [...set].sort();
}

export function loadDisabled(storage) {
  try {
    const raw = storage ? storage.getItem(PLUGIN_DISABLED_KEY) : null;
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

export function saveDisabled(storage, ids) {
  try {
    if (storage) storage.setItem(PLUGIN_DISABLED_KEY, JSON.stringify(Array.isArray(ids) ? ids : []));
    return true;
  } catch {
    return false;
  }
}

// 把 widget 的 endpoint 与 params 拼成实际请求路径。
export function widgetQuery(widget) {
  const ep = widget && typeof widget.endpoint === "string" ? widget.endpoint : "";
  const params = widget && widget.params && typeof widget.params === "object" ? widget.params : {};
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) qs.append(k, String(v));
  const s = qs.toString();
  if (!s) return ep;
  return ep + (ep.includes("?") ? "&" : "?") + s;
}

// 列表型响应的数据位置：默认取 items，也可用 root 指定（如 topn 的 by_ip）。
export function extractRows(payload, root) {
  if (Array.isArray(payload)) return payload;
  const key = typeof root === "string" && root ? root : "items";
  const v = payload && payload[key];
  return Array.isArray(v) ? v : [];
}

// 按单位格式化数值：bytes(1024 进制) / rate(bit 速率) / count / percent(0~100) / ratio(0~1)
export function formatUnit(value, unit) {
  if (value === null || value === undefined || value === "") return "-";
  const n = Number(value);
  const finite = Number.isFinite(n);
  switch (String(unit || "").toLowerCase()) {
    case "bytes":
      return finite ? fmtBytes(n) : String(value);
    case "rate":
      return finite ? fmtRate(n) : String(value);
    case "percent":
      return finite ? n.toFixed(2) + " %" : String(value);
    case "ratio":
      return finite ? (n * 100).toFixed(2) + " %" : String(value);
    default:
      return finite ? fmtNum(n) : String(value);
  }
}