// 曲线左右坐标轴的手动/自动设置（纯逻辑）。
// 左轴 = 带宽（内部单位 bit/s，界面按 Mb/s 填写），右轴 = 包速率（pps）。
// 值为 null 表示"自动"：使用按数据量级取整后的稳定上限，数字只在跨档位时变化。

export const AXIS_STORE_KEY = "netmon.axis.v1";
export const BPS_PER_MBPS = 1e6;
export const MBPS_LIMIT = 1e6; // 1 Tb/s
export const PPS_LIMIT = 1e9;

// 解析输入：空/非法/非正数 -> null（自动）；超过上限则夹取。
export function parseAxisInput(text, limit) {
  if (text === null || text === undefined) return null;
  const raw = String(text).trim();
  if (raw === "") return null;
  const n = Number(raw);
  if (!Number.isFinite(n) || n <= 0) return null;
  const cap = Number(limit) > 0 ? Number(limit) : Infinity;
  return Math.min(cap, n);
}

// 读取本地配置时的容错。
export function normalizeAxisConfig(raw) {
  const src = raw && typeof raw === "object" ? raw : {};
  return {
    mbps: parseAxisInput(src.mbps, MBPS_LIMIT),
    pps: parseAxisInput(src.pps, PPS_LIMIT),
    // 锁定：坐标轴取一次值后固定住，不随数据频繁变化（默认开启）
    lock: src.lock === undefined ? true : Boolean(src.lock),
  };
}

// 生成 ECharts 需要的轴上限；undefined 表示交给自动计算。
export function axisMax(config) {
  const c = normalizeAxisConfig(config);
  return {
    left: c.mbps === null ? undefined : c.mbps * BPS_PER_MBPS,
    right: c.pps === null ? undefined : c.pps,
    manual: c.mbps !== null || c.pps !== null,
  };
}

// 按 1/2/5×10^n 向上取整：自动模式下坐标轴只在跨档位时变化，不会一直跳。
export function niceCeil(v) {
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 0) return 1;
  const exp = Math.floor(Math.log10(n));
  const base = Math.pow(10, exp);
  const frac = n / base;
  const step = frac <= 1 ? 1 : frac <= 2 ? 2 : frac <= 5 ? 5 : 10;
  return step * base;
}

// 自动上限：数据最大值上浮 10% 后取整。
export function autoAxisMax(dataMax) {
  const m = Number(dataMax);
  if (!Number.isFinite(m) || m <= 0) return 1;
  return niceCeil(m * 1.1);
}

// 计算最终使用的轴上限：手动值 > 已冻结值（锁定开启时）> 按当前数据自动计算。
// 返回 auto 便于调用方在锁定模式下"取一次值并冻结"。
export function axisLimits(config, dataMax, frozen) {
  const c = normalizeAxisConfig(config);
  const d = dataMax && typeof dataMax === "object" ? dataMax : {};
  const auto = {
    left: autoAxisMax(d.bps),
    right: autoAxisMax(d.pps),
  };
  const manualLeft = c.mbps === null ? null : c.mbps * BPS_PER_MBPS;
  const manualRight = c.pps === null ? null : c.pps;
  const f = frozen && typeof frozen === "object" ? frozen : {};
  const left = manualLeft !== null ? manualLeft : c.lock && f.left ? f.left : auto.left;
  const right = manualRight !== null ? manualRight : c.lock && f.right ? f.right : auto.right;
  return { left, right, auto, locked: c.lock, manual: manualLeft !== null || manualRight !== null };
}

// 坐标轴状态文案。
export function axisHint(config) {
  const c = normalizeAxisConfig(config);
  if (c.mbps === null && c.pps === null) {
    return c.lock ? "坐标轴：锁定（取一次值后固定，不随数据跳动）" : "坐标轴：自动（按量级取整）";
  }
  const parts = [];
  if (c.mbps !== null) parts.push("左轴 " + c.mbps + " Mb/s");
  if (c.pps !== null) parts.push("右轴 " + c.pps + " pps");
  return "坐标轴：手动固定（" + parts.join("，") + "）";
}