import { test } from "node:test";
import assert from "node:assert/strict";
import { CHART_MODES, chartModeLabel, normalizeChartMode } from "../src/chartmode.js";

test("柱状图已移除：bar 回落到折线", () => {
  assert.deepEqual(CHART_MODES, ["line", "area"]);
  assert.equal(normalizeChartMode("bar"), "line");
  assert.equal(CHART_MODES.includes("bar"), false);
});

test("normalizeChartMode 容错", () => {
  assert.equal(normalizeChartMode("line"), "line");
  assert.equal(normalizeChartMode("area"), "area");
  assert.equal(normalizeChartMode(""), "line");
  assert.equal(normalizeChartMode(null), "line");
  assert.equal(normalizeChartMode(undefined), "line");
  assert.equal(normalizeChartMode("柱状"), "line");
});

test("chartModeLabel 输出中文名", () => {
  assert.equal(chartModeLabel("line"), "折线图");
  assert.equal(chartModeLabel("area"), "面积图");
  assert.equal(chartModeLabel("bar"), "折线图");
});