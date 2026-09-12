// 让曲线与上方 KPI 数值同步：把每次实时轮询的采样补进曲线末尾（纯函数）。

export const LIVE_MAX_POINTS = 720;

// 把秒级时间戳对齐到 step 秒的时间桶。
export function bucketStart(tsSec, stepSec) {
  const step = Math.max(1, Math.floor(Number(stepSec) || 1));
  const t = Math.floor(Number(tsSec) || 0);
  if (!t) return 0;
  return Math.floor(t / step) * step;
}

// 合并一个实时采样：
// - 与最后一个点落在同一个时间桶内 -> 覆盖该点（避免同一桶出现重复点）；
// - 否则追加新点；
// - 超过 maxPoints 时从头裁剪，保证长时间运行不会无限增长。
// 返回新数组，不修改入参。
export function mergeLiveSample(points, sample, opts) {
  const list = Array.isArray(points) ? points.map((p) => ({ ...p })) : [];
  const o = opts && typeof opts === "object" ? opts : {};
  const step = Math.max(1, Math.floor(Number(o.step) || 1));
  const maxPoints = Math.max(2, Math.floor(Number(o.maxPoints) || LIVE_MAX_POINTS));
  const t = Math.floor(Number(sample && sample.t) || 0);
  if (!t) return list;
  const bps = Number(sample && sample.bps) || 0;
  const pps = Number(sample && sample.pps) || 0;
  const last = list.length ? list[list.length - 1] : null;
  if (last && t - Math.floor(Number(last.t) || 0) < step) {
    list[list.length - 1] = { ...last, bps, pps };
  } else {
    list.push({ t, bps, pps });
  }
  if (list.length > maxPoints) list.splice(0, list.length - maxPoints);
  return list;
}