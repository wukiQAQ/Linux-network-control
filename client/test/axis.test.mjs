import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BPS_PER_MBPS,
  axisLimits,
  MBPS_LIMIT,
  axisHint,
  axisMax,
  autoAxisMax,
  niceCeil,
  normalizeAxisConfig,
  parseAxisInput,
} from "../src/axis.js";

test("parseAxisInput：空或非法输入表示自动", () => {
  assert.equal(parseAxisInput("", MBPS_LIMIT), null);
  assert.equal(parseAxisInput("   ", MBPS_LIMIT), null);
  assert.equal(parseAxisInput("abc", MBPS_LIMIT), null);
  assert.equal(parseAxisInput("-5", MBPS_LIMIT), null);
  assert.equal(parseAxisInput("0", MBPS_LIMIT), null);
  assert.equal(parseAxisInput(null, MBPS_LIMIT), null);
});

test("parseAxisInput：正常数值与上限夹取", () => {
  assert.equal(parseAxisInput("50", MBPS_LIMIT), 50);
  assert.equal(parseAxisInput(" 12.5 ", MBPS_LIMIT), 12.5);
  assert.equal(parseAxisInput("99999999", MBPS_LIMIT), MBPS_LIMIT);
});

test("normalizeAxisConfig 容错", () => {
  assert.deepEqual(normalizeAxisConfig({ mbps: "10", pps: "200" }), { mbps: 10, pps: 200, lock: true });
  assert.deepEqual(normalizeAxisConfig(null), { mbps: null, pps: null, lock: true });
  assert.deepEqual(normalizeAxisConfig({ mbps: "x" }), { mbps: null, pps: null, lock: true });
  assert.equal(normalizeAxisConfig({ lock: false }).lock, false);
});

test("axisMax：手动值换算与自动标记", () => {
  const manual = axisMax({ mbps: 50, pps: null });
  assert.equal(manual.left, 50 * BPS_PER_MBPS);
  assert.equal(manual.right, undefined);
  assert.equal(manual.manual, true);
  const right = axisMax({ mbps: null, pps: 1200 });
  assert.equal(right.left, undefined);
  assert.equal(right.right, 1200);
  const auto = axisMax({});
  assert.equal(auto.left, undefined);
  assert.equal(auto.right, undefined);
  assert.equal(auto.manual, false);
});

test("niceCeil 只在档位变化时改变坐标轴", () => {
  assert.equal(niceCeil(0.7), 1);
  assert.equal(niceCeil(1), 1);
  assert.equal(niceCeil(1.2), 2);
  assert.equal(niceCeil(3), 5);
  assert.equal(niceCeil(120), 200);
  assert.equal(niceCeil(0), 1);
  assert.equal(niceCeil("x"), 1);
});

test("autoAxisMax 上浮 10% 后取整", () => {
  assert.equal(autoAxisMax(0), 1);
  assert.equal(autoAxisMax(1000000), 2000000);
  assert.equal(autoAxisMax(900000), 1000000);
});

test("axisHint 文案", () => {
  // 默认锁定：不再随数据跳动
  assert.ok(axisHint({}).includes("锁定"));
  assert.ok(axisHint({ lock: false }).includes("自动"));
  const manual = axisHint({ mbps: 50, pps: 800 });
  assert.ok(manual.includes("手动"));
  assert.ok(manual.includes("左轴 50 Mb/s"));
  assert.ok(manual.includes("右轴 800 pps"));
});

test("axisLimits：锁定后坐标轴不再随数据变化", () => {
  const cfg = { lock: true };
  const first = axisLimits(cfg, { bps: 1000000, pps: 500 }, {});
  assert.equal(first.left, 2000000); // 1 Mb/s 上浮取整到 2 Mb
  // 数据翻倍，但仍在锁定值以内 -> 轴不变
  const frozen = { left: first.left, right: first.right };
  const second = axisLimits(cfg, { bps: 1800000, pps: 900 }, frozen);
  assert.equal(second.left, first.left);
  assert.equal(second.right, first.right);
});

test("axisLimits：关闭锁定则跟随数据", () => {
  const cfg = { lock: false };
  const a = axisLimits(cfg, { bps: 1000000, pps: 100 }, { left: 111, right: 222 });
  assert.equal(a.left, 2000000);
  assert.notEqual(a.right, 222);
});

test("axisLimits：手动值优先于锁定值与自动值", () => {
  const cfg = { mbps: 5, lock: true };
  const r = axisLimits(cfg, { bps: 1000000, pps: 100 }, { left: 999, right: 888 });
  assert.equal(r.left, 5 * BPS_PER_MBPS);
  assert.equal(r.right, 888); // 右轴未填 -> 用锁定值
  assert.equal(r.manual, true);
});
