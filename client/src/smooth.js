// 曲线显示平滑：只影响"看到的形状"，不改服务端数据与统计口径。
// 用居中滑动平均（窗口为奇数，边界按可用点平均，避免端点被拉低）。

export const SMOOTH_LEVELS = [
  { value: "off", label: "关", window: 1 },
  { value: "low", label: "轻", window: 3 },
  { value: "mid", label: "中", window: 5 },
  { value: "high", label: "强", window: 9 },
];

export function smoothWindow(level) {
  const hit = SMOOTH_LEVELS.find((l) => l.value === level);
  return hit ? hit.window : 1;
}

export function smoothLabel(level) {
  const hit = SMOOTH_LEVELS.find((l) => l.value === level);
  return hit ? hit.label : "关";
}

// 对 bps / pps 做居中滑动平均；off 或点太少时只做浅拷贝返回。
export function smoothSeries(points, level) {
  const list = Array.isArray(points) ? points : [];
  const w = smoothWindow(level);
  if (w <= 1 || list.length < 3) return list.map((p) => ({ ...p }));
  const half = Math.floor(w / 2);
  return list.map((p, i) => {
    const from = Math.max(0, i - half);
    const to = Math.min(list.length - 1, i + half);
    let n = 0;
    let bps = 0;
    let pps = 0;
    for (let j = from; j <= to; j += 1) {
      bps += Number(list[j] && list[j].bps) || 0;
      pps += Number(list[j] && list[j].pps) || 0;
      n += 1;
    }
    return { ...p, bps: bps / n, pps: pps / n };
  });
}