import { test } from "node:test";
import assert from "node:assert/strict";
import { TOPN_RANGES, barWidth, entryLabel, entrySummary, panelTitle, shortBytes } from "../src/topn.js";

test("barWidth 夹取范围", () => {
  assert.equal(barWidth(0), "2%");
  assert.equal(barWidth(45.26), "45.3%");
  assert.equal(barWidth(200), "100.0%");
  assert.equal(barWidth("x"), "2%");
});

test("shortBytes 单位换算", () => {
  assert.equal(shortBytes(512), "512 B");
  assert.equal(shortBytes(2048), "2.0 KB");
  assert.equal(shortBytes(5 * 1024 * 1024), "5.0 MB");
});

test("entrySummary 组合摘要", () => {
  const s = entrySummary({ bytes: 1500, flows: 3, percent: 44.12 });
  assert.ok(s.includes("1.5 KB"));
  assert.ok(s.includes("3 会话"));
  assert.ok(s.includes("44.1%"));
});

test("entryLabel 与 panelTitle", () => {
  assert.equal(entryLabel("port", "443"), ":443");
  assert.equal(entryLabel("ip", "10.0.0.1"), "10.0.0.1");
  assert.ok(panelTitle({ total_flows: 3, total_bytes: 1700 }).includes("会话 3 条"));
  assert.equal(TOPN_RANGES.length, 2);
});