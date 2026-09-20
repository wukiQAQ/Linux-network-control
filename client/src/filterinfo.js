// 服务端采集过滤（capture.filter）的展示逻辑（纯函数，便于单元测试）。
// 服务端启用过滤时，/api/v1/traffic/now 会带上 filter（表达式）与 filter_dropped（已过滤帧数）；
// 客户端据此说明"当前过滤了什么、过滤掉多少帧"，避免把"流量不全"误判成采集故障。

export function filterSummary(now) {
  const expr = now && typeof now.filter === "string" ? now.filter.trim() : "";
  if (!expr) return { active: false, text: "未启用（服务端全量采集）" };
  const dropped = now && typeof now.filter_dropped === "number" ? now.filter_dropped : null;
  return {
    active: true,
    text: dropped === null ? "服务端正在过滤：" + expr : "服务端正在过滤：" + expr + "（已过滤 " + dropped + " 帧）",
  };
}