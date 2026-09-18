// 多机（fleet）视图的纯逻辑：状态判定、排序、汇总与徽标。

export function hostStatus(p) {
  if (!p || !p.ok) return { key: "offline", text: "离线", cls: "bad" };
  return { key: "online", text: "在线", cls: "ok" };
}

export function hostVersion(p) {
  const v = p && p.now && typeof p.now.version === "string" ? p.now.version : "";
  return v ? "V" + v : "未上报";
}

// 把能力清单转成简短徽标
export function featureBadges(p) {
  const list = p && p.now && Array.isArray(p.now.features) ? p.now.features : [];
  if (!list.length) return [];
  const map = [
    ["capture.dump", "抓包"],
    ["actions.run", "运维"],
    ["files", "文件"],
    ["stream", "实时"],
    ["alerts", "告警"],
  ];
  return map.filter(([k]) => list.includes(k)).map(([, label]) => label);
}

export function sortHosts(rows) {
  const arr = Array.isArray(rows) ? [...rows] : [];
  return arr.sort((a, b) => {
    const ao = a && a.probe && a.probe.ok ? 1 : 0;
    const bo = b && b.probe && b.probe.ok ? 1 : 0;
    if (ao !== bo) return bo - ao;
    return String((a && a.name) || "").localeCompare(String((b && b.name) || ""), "zh-CN");
  });
}

export function fleetSummary(rows) {
  const arr = Array.isArray(rows) ? rows : [];
  const online = arr.filter((r) => r && r.probe && r.probe.ok).length;
  return { total: arr.length, online, offline: arr.length - online };
}

export function hostMetrics(p) {
  const n = p && p.now ? p.now : null;
  if (!n) return "-";
  const bps = Number(n.bps) || 0;
  const pps = Math.round(Number(n.pps) || 0);
  const conns = Math.round(Number(n.conns) || 0);
  const up = Number(n.uptime_s) || 0;
  const rate = bps >= 1e6 ? (bps / 1e6).toFixed(1) + " Mb/s" : (bps / 1e3).toFixed(0) + " Kb/s";
  const h = Math.floor(up / 3600);
  const m = Math.floor((up % 3600) / 60);
  return rate + " · " + pps + " pps · " + conns + " 连接 · 运行 " + h + "h" + m + "m";
}