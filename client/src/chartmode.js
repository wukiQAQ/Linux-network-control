// 曲线样式：按需求只保留折线与面积（柱状图已移除）。
export const CHART_MODES = ["line", "area"];
export const CHART_MODE_LABELS = { line: "折线图", area: "面积图" };

// 容错：历史数据里若存的是已下线的 "bar"，回落到折线。
export function normalizeChartMode(mode) {
  return CHART_MODES.includes(mode) ? mode : "line";
}

export function chartModeLabel(mode) {
  return CHART_MODE_LABELS[normalizeChartMode(mode)] || "折线图";
}