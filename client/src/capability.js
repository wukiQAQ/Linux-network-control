// 服务端能力判断：客户端据此决定"抓包导出"等按钮是否可用，并给出可操作的提示。

export function serverVersion(now) {
  return now && typeof now.version === "string" ? now.version : "";
}

export function hasFeature(now, feature) {
  const list = now && Array.isArray(now.features) ? now.features : null;
  return Array.isArray(list) && list.includes(feature);
}

// 抓包导出需要服务端提供 capture.dump 能力（V0.7.0 及以上）。
export function captureSupport(now) {
  if (hasFeature(now, "capture.dump")) return { ok: true, hint: "" };
  const v = serverVersion(now);
  return {
    ok: false,
    hint: v
      ? "服务端为 V" + v + "，不支持抓包导出（需 V0.7.0+），请更新 Linux 端 netmon"
      : "服务端未上报版本/能力，抓包导出不可用（需 V0.7.0+），请更新 Linux 端 netmon",
  };
}