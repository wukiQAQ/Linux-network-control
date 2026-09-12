import { test } from "node:test";
import assert from "node:assert/strict";
import { ZOOM_MIN_SPAN, clampRange, zoomLabel, zoomRange } from "../src/chartzoom.js";

test("clampRange 夹取越界区间", () => {
  assert.deepEqual(clampRange(-20, 300), { start: 0, end: 100 });
  assert.deepEqual(clampRange(40, 60), { start: 40, end: 60 });
});

test("clampRange 保证不小于最小窗口", () => {
  const r = clampRange(50, 51);
  assert.equal(r.end - r.start, ZOOM_MIN_SPAN);
  const tail = clampRange(99, 100);
  assert.equal(tail.end, 100);
  assert.equal(tail.end - tail.start, ZOOM_MIN_SPAN);
});

test("zoomRange 放大围绕中心收缩窗口", () => {
  const r = zoomRange({ start: 0, end: 100 }, "in");
  assert.equal(Math.round(r.end - r.start), 60);
  assert.equal(Math.round(r.start), 20);
  assert.equal(Math.round(r.end), 80);
});

test("zoomRange 缩小恢复窗口，重置回到全量", () => {
  const mid = zoomRange({ start: 20, end: 80 }, "out");
  assert.deepEqual(mid, { start: 0, end: 100 });
  assert.deepEqual(zoomRange({ start: 40, end: 50 }, "reset"), { start: 0, end: 100 });
});

test("zoomRange 连续放大不会低于最小窗口", () => {
  let r = { start: 0, end: 100 };
  for (let i = 0; i < 20; i += 1) r = zoomRange(r, "in");
  assert.ok(r.end - r.start >= ZOOM_MIN_SPAN - 0.01);
  assert.ok(r.start >= 0 && r.end <= 100);
});

test("zoomRange 边界处自动平移，窗口宽度保持", () => {
  const left = zoomRange({ start: 0, end: 10 }, "out");
  assert.equal(left.start, 0);
  assert.equal(Math.round((left.end - left.start) * 100) / 100, 16.67);
  const right = zoomRange({ start: 90, end: 100 }, "out");
  assert.equal(right.end, 100);
  assert.equal(Math.round((right.end - right.start) * 100) / 100, 16.67);
});

test("zoomLabel 输出缩放文案", () => {
  assert.equal(zoomLabel({ start: 0, end: 100 }), "全量");
  assert.equal(zoomLabel({ start: 0, end: 60 }), "显示 60%");
  assert.equal(zoomLabel(null), "全量");
});