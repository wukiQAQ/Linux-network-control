import { test } from "node:test";
import assert from "node:assert/strict";
import { SMOOTH_LEVELS, smoothLabel, smoothSeries, smoothWindow } from "../src/smooth.js";

test("smoothWindow / smoothLabel 映射", () => {
  assert.equal(smoothWindow("off"), 1);
  assert.equal(smoothWindow("low"), 3);
  assert.equal(smoothWindow("mid"), 5);
  assert.equal(smoothWindow("high"), 9);
  assert.equal(smoothWindow("unknown"), 1);
  assert.equal(smoothLabel("mid"), "中");
  assert.equal(SMOOTH_LEVELS.length, 4);
});

test("off 或点太少时保持原值（且不共享引用）", () => {
  const pts = [{ t: 1, bps: 10, pps: 1 }, { t: 2, bps: 20, pps: 2 }];
  const out = smoothSeries(pts, "off");
  assert.deepEqual(out, pts);
  assert.notEqual(out[0], pts[0]);
  assert.deepEqual(smoothSeries(pts, "high"), pts);
});

test("轻度平滑削掉单个毛刺", () => {
  const pts = [
    { t: 1, bps: 100, pps: 10 },
    { t: 2, bps: 100, pps: 10 },
    { t: 3, bps: 1000, pps: 100 },
    { t: 4, bps: 100, pps: 10 },
    { t: 5, bps: 100, pps: 10 },
  ];
  const out = smoothSeries(pts, "low");
  assert.equal(out[2].bps, 400); // (100+1000+100)/3
  assert.ok(out[2].bps < 1000);
  assert.equal(out[0].bps, 100); // 端点按可用点平均，不被拉低
  assert.equal(out[4].pps, 10);
});

test("中度平滑窗口更大、起伏更平缓", () => {
  const pts = [0, 0, 100, 0, 0].map((v, i) => ({ t: i + 1, bps: v, pps: v }));
  const mid = smoothSeries(pts, "mid");
  assert.equal(mid[2].bps, 20); // (0+0+100+0+0)/5
  const low = smoothSeries(pts, "low");
  assert.ok(low[2].bps > mid[2].bps);
});

test("不修改入参、保留时间戳", () => {
  const pts = [{ t: 7, bps: 0, pps: 0 }, { t: 8, bps: 90, pps: 9 }, { t: 9, bps: 0, pps: 0 }];
  const out = smoothSeries(pts, "low");
  assert.deepEqual(pts[1], { t: 8, bps: 90, pps: 9 });
  assert.deepEqual(out.map((p) => p.t), [7, 8, 9]);
  assert.deepEqual(smoothSeries(null, "low"), []);
});