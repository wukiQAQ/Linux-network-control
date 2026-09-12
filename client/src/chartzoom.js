// 历史曲线缩放区间计算（纯函数，便于单元测试）。
// 区间用百分比表示，与 ECharts dataZoom 的 start/end 语义一致：0 = 最早，100 = 最新。

export const ZOOM_MIN_SPAN = 5; // 最小可视窗口：时间范围的 5%
export const ZOOM_STEP = 0.6; // 每次「放大」保留的窗口比例

function round2(v) {
  return Math.round(v * 100) / 100;
}

// 取值并回退到默认值；null/undefined/非数字都视为「未指定」，避免被当成 0。
function numOr(v, def) {
  if (v === null || v === undefined || v === "") return def;
  const n = Number(v);
  return Number.isFinite(n) ? n : def;
}

// 把区间夹到 [0,100] 并保证不小于最小窗口，避免出现空窗口或越界。
export function clampRange(start, end) {
  let s = Math.min(100, Math.max(0, numOr(start, 0)));
  let e = Math.min(100, Math.max(0, numOr(end, 100)));
  if (e - s < ZOOM_MIN_SPAN) {
    if (s + ZOOM_MIN_SPAN <= 100) {
      e = s + ZOOM_MIN_SPAN;
    } else {
      e = 100;
      s = 100 - ZOOM_MIN_SPAN;
    }
  }
  return { start: round2(s), end: round2(e) };
}

// direction: "in" 放大（窗口变小）、"out" 缩小（窗口变大）、其他值复位。
// 缩放围绕当前窗口中心进行，碰到边界时自动平移，保证窗口宽度不变。
export function zoomRange(cur, direction) {
  const c = clampRange(cur && cur.start, cur && cur.end);
  if (direction !== "in" && direction !== "out") return { start: 0, end: 100 };
  const span = c.end - c.start;
  const center = (c.start + c.end) / 2;
  const raw = direction === "in" ? span * ZOOM_STEP : span / ZOOM_STEP;
  const next = Math.min(100, Math.max(ZOOM_MIN_SPAN, raw));
  let s = center - next / 2;
  let e = center + next / 2;
  if (s < 0) {
    e -= s;
    s = 0;
  }
  if (e > 100) {
    s -= e - 100;
    e = 100;
  }
  return clampRange(s, e);
}

// 缩放状态文案。
export function zoomLabel(range) {
  const r = clampRange(range && range.start, range && range.end);
  const span = Math.round(r.end - r.start);
  return span >= 99 ? "全量" : "显示 " + span + "%";
}