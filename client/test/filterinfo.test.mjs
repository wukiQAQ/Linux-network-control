import { test } from "node:test";
import assert from "node:assert/strict";
import { filterSummary } from "../src/filterinfo.js";

test("未启用过滤时给出全量采集提示", () => {
  for (const now of [{}, null, { filter: "" }, { filter: "   " }]) {
    const s = filterSummary(now);
    assert.equal(s.active, false);
    assert.ok(s.text.includes("未启用"));
  }
});

test("启用过滤时展示表达式与已过滤帧数", () => {
  const s = filterSummary({ filter: "tcp and dst port 443", filter_dropped: 6201 });
  assert.equal(s.active, true);
  assert.ok(s.text.includes("tcp and dst port 443"));
  assert.ok(s.text.includes("6201"));
});

test("缺少数值字段时只展示表达式", () => {
  const s = filterSummary({ filter: " udp " });
  assert.equal(s.active, true);
  assert.ok(s.text.includes("udp"));
  assert.ok(!s.text.includes("NaN"));
  assert.ok(!s.text.includes("null"));
});