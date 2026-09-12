import { test } from "node:test";
import assert from "node:assert/strict";
import { LIVE_MAX_POINTS, bucketStart, mergeLiveSample } from "../src/livechart.js";

test("bucketStart 对齐到时间桶", () => {
  assert.equal(bucketStart(1700000012, 10), 1700000010);
  assert.equal(bucketStart(1700000010, 10), 1700000010);
  assert.equal(bucketStart(0, 10), 0);
  // step 非法（0）时按 1 秒处理，因此不改动时间戳
  assert.equal(bucketStart("1700000012", 0), 1700000012);
});

test("空曲线追加第一个点", () => {
  const out = mergeLiveSample([], { t: 1700000000, bps: 100, pps: 5 }, { step: 10 });
  assert.deepEqual(out, [{ t: 1700000000, bps: 100, pps: 5 }]);
});

test("同一个时间桶内覆盖最后一个点（曲线与 KPI 保持一致）", () => {
  const points = [{ t: 1700000000, bps: 100, pps: 5 }];
  const out = mergeLiveSample(points, { t: 1700000004, bps: 250, pps: 9 }, { step: 10 });
  assert.equal(out.length, 1);
  assert.deepEqual(out[0], { t: 1700000000, bps: 250, pps: 9 });
  // 原数组不被修改
  assert.deepEqual(points[0], { t: 1700000000, bps: 100, pps: 5 });
});

test("跨到下一个桶时追加新点", () => {
  const points = [{ t: 1700000000, bps: 100, pps: 5 }];
  const out = mergeLiveSample(points, { t: 1700000010, bps: 300, pps: 12 }, { step: 10 });
  assert.equal(out.length, 2);
  assert.deepEqual(out[1], { t: 1700000010, bps: 300, pps: 12 });
});

test("超出上限时从头裁剪", () => {
  const points = Array.from({ length: 5 }, (_, i) => ({ t: 1000 + i * 10, bps: i, pps: i }));
  const out = mergeLiveSample(points, { t: 2000, bps: 9, pps: 9 }, { step: 10, maxPoints: 3 });
  assert.equal(out.length, 3);
  assert.equal(out[out.length - 1].t, 2000);
});

test("非法输入容错且不抛异常", () => {
  assert.deepEqual(mergeLiveSample(null, null), []);
  assert.deepEqual(mergeLiveSample(undefined, { t: 0 }), []);
  const out = mergeLiveSample([{ t: 5, bps: 1, pps: 1 }], { t: 6, bps: "x", pps: null }, { step: 1 });
  assert.equal(out.length, 2);
  assert.equal(out[1].bps, 0);
  assert.equal(out[1].pps, 0);
  assert.ok(LIVE_MAX_POINTS >= 100);
});