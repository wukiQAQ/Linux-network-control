// 账号管理：已保存连接的纯逻辑（不依赖 DOM，便于单元测试）。
// 设计要点：以「规范化后的服务器地址」作为唯一键，因此同一个人连过的 IP
// 只会保留一条记录，连接成功后可自动写入这里。

export const PROFILE_STORE_KEY = "netmon.profiles.v1";

// 连接的唯一标识：去掉首尾空白与结尾斜杠，使 http://host:8080/ 与 http://host:8080 视为同一条。
export function profileKey(base) {
  const s = base === undefined || base === null ? "" : String(base);
  return s.trim().replace(/\/+$/, "");
}

// 展示名：优先用户填写的名称，其次退回服务器地址。
export function profileName(p) {
  const name = p && typeof p.name === "string" ? p.name.trim() : "";
  const key = profileKey(p && p.base);
  return name || key || "未命名连接";
}

// 生成一条标准记录；lastUsed 为最近一次成功连接的时间戳（毫秒）。
export function makeProfile(base, opts) {
  const o = opts && typeof opts === "object" ? opts : {};
  const lastUsed = Number(o.lastUsed);
  return {
    name: typeof o.name === "string" ? o.name.trim() : "",
    base: profileKey(base),
    token: typeof o.token === "string" ? o.token : "",
    lastUsed: Number.isFinite(lastUsed) ? lastUsed : 0,
  };
}

// 按最近使用时间倒序；时间相同则按名称排序，保证界面顺序稳定。
export function sortProfiles(list) {
  const arr = Array.isArray(list) ? [...list] : [];
  return arr.sort((a, b) => {
    const d = (b.lastUsed || 0) - (a.lastUsed || 0);
    if (d !== 0) return d;
    return profileName(a).localeCompare(profileName(b), "zh-CN");
  });
}

// 读取本地数据时的容错：丢弃无地址的记录，按地址去重并保留最近使用时间。
export function normalizeProfiles(raw) {
  if (!Array.isArray(raw)) return [];
  const map = new Map();
  raw.forEach((item) => {
    const p = makeProfile(item && item.base, item || {});
    if (!p.base) return;
    const old = map.get(p.base);
    if (!old || old.lastUsed <= p.lastUsed) map.set(p.base, p);
  });
  return sortProfiles([...map.values()]);
}

// 新增或更新一条记录：连接成功后调用，实现「连过的 IP 自动进入账号管理」。
export function upsertProfile(list, entry) {
  const incoming = makeProfile(entry && entry.base, entry || {});
  const arr = Array.isArray(list) ? list : [];
  if (!incoming.base) return [...arr];
  const kept = arr.filter((x) => profileKey(x && x.base) !== incoming.base);
  return sortProfiles([...kept, incoming]);
}

// 按地址删除记录。
export function removeProfile(list, key) {
  const k = profileKey(key);
  const arr = Array.isArray(list) ? list : [];
  return arr.filter((x) => profileKey(x && x.base) !== k);
}

// 按地址查找记录（未找到返回 null）。
export function findProfile(list, key) {
  const k = profileKey(key);
  const arr = Array.isArray(list) ? list : [];
  return arr.find((x) => profileKey(x && x.base) === k) || null;
}

// 「最近连接」文案；now 可注入，便于单元测试。
export function lastUsedText(ts, now) {
  const t = Number(ts) || 0;
  if (!t) return "未连接过";
  const base = Number(now) || Date.now();
  const diff = Math.max(0, Math.floor((base - t) / 1000));
  if (diff < 60) return "刚刚连接";
  if (diff < 3600) return Math.floor(diff / 60) + " 分钟前连接";
  if (diff < 86400) return Math.floor(diff / 3600) + " 小时前连接";
  return Math.floor(diff / 86400) + " 天前连接";
}