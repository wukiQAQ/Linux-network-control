// 纯函数格式化与校验工具（独立于 DOM，便于单元测试）

const BIT_UNITS = ["b/s", "kb/s", "Mb/s", "Gb/s", "Tb/s"];
const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"];
const NUM_UNITS = ["", "k", "M", "G"];

export function normalizeBaseUrl(raw) {
  if (raw === undefined || raw === null || String(raw).trim() === "") {
    return { ok: false, error: "请输入服务器地址" };
  }
  let s = String(raw).trim();
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(s)) {
    s = "http://" + s;
  } else if (!/^https?:\/\//i.test(s)) {
    return { ok: false, error: "仅支持 http/https 协议" };
  }
  try {
    const u = new URL(s);
    const host = u.hostname;
    const hostRe =
      /^(\[[0-9a-fA-F:.]+\]|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*)$/;
    if (!host || !hostRe.test(host)) {
      return { ok: false, error: "地址格式不正确，示例：192.168.1.100:8080 或 http://host:8080" };
    }
    u.pathname = "";
    u.search = "";
    u.hash = "";
    return { ok: true, url: u.toString().replace(/\/$/, "") };
  } catch {
    return { ok: false, error: "地址格式不正确，示例：192.168.1.100:8080 或 http://host:8080" };
  }
}

// 将数值缩放到合适单位，返回 { text, unit }。
function scaleUnit(value, units, digits) {
  const n = Number(value);
  if (!Number.isFinite(n)) return { text: "-", unit: "" };
  if (n === 0) return { text: "0", unit: units[0] };
  const sign = n < 0 ? -1 : 1;
  let scaled = Math.abs(n);
  let i = 0;
  while (scaled >= 1000 && i < units.length - 1) {
    scaled /= 1000;
    i++;
  }
  const val = sign * scaled;
  let text = Math.abs(val) >= 100 ? val.toFixed(0) : val.toFixed(digits);
  if (text.includes(".")) {
    text = text.replace(/0+$/, "").replace(/\.$/, "");
  }
  return { text, unit: units[i] };
}

function formatWithUnits(value, units, digits = 2) {
  const { text, unit } = scaleUnit(value, units, digits);
  return unit ? `${text} ${unit}` : text;
}

export function fmtRate(bps) {
  return formatWithUnits(bps, BIT_UNITS);
}

export function fmtBytes(bytes) {
  return formatWithUnits(bytes, BYTE_UNITS);
}

export function fmtPps(pps) {
  const { text, unit } = scaleUnit(pps, NUM_UNITS, 2);
  return (unit ? text + unit : text) + " pps";
}

export function fmtNum(n) {
  const { text, unit } = scaleUnit(n, NUM_UNITS, 1);
  return unit ? text + unit : text;
}

export function fmtTs(iso) {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  const p = (x) => String(x).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export function fmtDuration(sec) {
  const s = Math.max(0, Math.floor(Number(sec) || 0));
  if (s < 60) return `${s} 秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分 ${s % 60} 秒`;
  const h = Math.floor(m / 60);
  return `${h} 小时 ${m % 60} 分`;
}

export function protoLabel(proto) {
  const map = { tcp: "TCP", udp: "UDP", icmp: "ICMP" };
  return map[String(proto).toLowerCase()] || String(proto).toUpperCase();
}