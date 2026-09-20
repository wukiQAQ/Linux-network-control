import { test } from "node:test";
import assert from "node:assert/strict";
import {
  PLUGIN_DISABLED_KEY,
  extractRows,
  formatUnit,
  loadDisabled,
  navKey,
  normalizePlugins,
  pluginAvailability,
  pluginIdFromNavKey,
  pluginList,
  saveDisabled,
  safeEndpoint,
  togglePluginDisabled,
  validatePlugin,
  widgetQuery,
} from "../src/pluginregistry.js";

const okSpec = {
  id: "demo-panel",
  title: "演示面板",
  order: 10,
  widgets: [
    { type: "kpi", fields: [{ key: "bps", unit: "rate" }] },
    { type: "text", text: "说明文字" },
  ],
};

test("validatePlugin 拒绝畸形与越界清单", () => {
  assert.equal(validatePlugin(okSpec).ok, true);
  const bad = [
    null,
    { ...okSpec, id: "Bad ID" },
    { ...okSpec, id: "a" },
    { ...okSpec, title: "   " },
    { ...okSpec, widgets: [] },
    { ...okSpec, widgets: [{ type: "iframe", endpoint: "/api/v1/traffic/now" }] },
    { ...okSpec, widgets: [{ type: "text", text: "   " }] },
    { ...okSpec, widgets: [{ type: "kpi", fields: [{ key: "", unit: "rate" }] }] },
    { ...okSpec, widgets: [{ type: "kpi", fields: [{ key: "bps", unit: "usd" }] }] },
    { ...okSpec, widgets: [{ type: "table", endpoint: "http://evil.example/x", columns: [{ key: "a" }] }] },
    { ...okSpec, widgets: [{ type: "table", endpoint: "/api/v1/topn" }] },
  ];
  for (const spec of bad) {
    assert.equal(validatePlugin(spec).ok, false, JSON.stringify(spec));
  }
});

test("safeEndpoint 只允许本机监控接口", () => {
  assert.equal(safeEndpoint("/api/v1/topn"), true);
  assert.equal(safeEndpoint("/api/v1/topn?n=5"), true);
  assert.equal(safeEndpoint("http://127.0.0.1/api/v1/x"), false);
  assert.equal(safeEndpoint("//api/v1/x"), false);
  assert.equal(safeEndpoint("/api/v2/x"), false);
  assert.equal(safeEndpoint("/api/v1//x"), false);
  assert.equal(safeEndpoint("/api/v1/x y"), false);
  assert.equal(safeEndpoint("\\api\\v1\\x"), false);
  assert.equal(safeEndpoint(42), false);
});

test("normalizePlugins 去重、排序并补默认值", () => {
  const list = normalizePlugins([
    { id: "b-panel", title: "B 面板", order: 20, widgets: [{ type: "text", text: "b" }] },
    { id: "a-panel", title: "A 面板", order: 10, widgets: [{ type: "text", text: "a" }] },
    { id: "a-panel", title: "重复的 A", order: 1, widgets: [{ type: "text", text: "dup" }] },
    { id: "Bad", title: "非法", widgets: [{ type: "text", text: "x" }] },
  ]);
  assert.deepEqual(
    list.map((p) => p.id),
    ["a-panel", "b-panel"],
  );
  assert.equal(list[0].icon, "🧩");
  assert.equal(list[0].widgets[0].root, "items");
  assert.deepEqual(list[0].requires, []);
});

test("pluginAvailability 按服务端能力门控", () => {
  const spec = { ...okSpec, requires: ["topn", "sessions"] };
  const bad = pluginAvailability(spec, ["topn"]);
  assert.equal(bad.ok, false);
  assert.deepEqual(bad.missing, ["sessions"]);
  assert.ok(bad.hint.includes("sessions"));
  assert.equal(pluginAvailability(spec, ["topn", "sessions"]).ok, true);
  assert.equal(pluginAvailability({}, null).ok, true);
});

test("pluginList 合并启用状态与可用性", () => {
  const rows = pluginList([{ ...okSpec, requires: ["topn"] }], { features: ["topn"], disabled: [] });
  assert.equal(rows[0].enabled, true);
  assert.equal(rows[0].available, true);
  const off = pluginList([okSpec], { features: [], disabled: [okSpec.id] });
  assert.equal(off[0].enabled, false);
  const missing = pluginList([{ ...okSpec, requires: ["topn"] }], { features: [] });
  assert.equal(missing[0].available, false);
  assert.deepEqual(missing[0].missing, ["topn"]);
});

test("启停状态可持久化且容错", () => {
  const store = new Map();
  const storage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
  };
  assert.deepEqual(loadDisabled(storage), []);
  assert.equal(saveDisabled(storage, ["a-panel", "b-panel"]), true);
  assert.equal(store.get(PLUGIN_DISABLED_KEY), JSON.stringify(["a-panel", "b-panel"]));
  assert.deepEqual(loadDisabled(storage), ["a-panel", "b-panel"]);
  assert.deepEqual(togglePluginDisabled(["a-panel"], "b-panel", false), ["a-panel", "b-panel"]);
  assert.deepEqual(togglePluginDisabled(["a-panel", "b-panel"], "a-panel", true), ["b-panel"]);
  assert.deepEqual(loadDisabled(null), []);
  store.set(PLUGIN_DISABLED_KEY, "{坏 JSON");
  assert.deepEqual(loadDisabled(storage), []);
  store.set(PLUGIN_DISABLED_KEY, JSON.stringify([1, "ok"]));
  assert.deepEqual(loadDisabled(storage), ["ok"]);
});

test("导航 key 与插件 id 互转", () => {
  assert.equal(navKey("p1"), "plugin:p1");
  assert.equal(pluginIdFromNavKey("plugin:p1"), "p1");
  assert.equal(pluginIdFromNavKey("dash"), "");
  assert.equal(pluginIdFromNavKey(null), "");
});

test("widgetQuery 拼接查询参数", () => {
  assert.equal(widgetQuery({ endpoint: "/api/v1/traffic/now" }), "/api/v1/traffic/now");
  assert.equal(widgetQuery({ endpoint: "/api/v1/topn", params: { since: "3600", n: "5" } }), "/api/v1/topn?since=3600&n=5");
  assert.equal(widgetQuery({ endpoint: "/api/v1/x?a=1", params: { b: "2" } }), "/api/v1/x?a=1&b=2");
  assert.equal(widgetQuery(null), "");
});

test("extractRows 兼容多种响应形状", () => {
  assert.deepEqual(extractRows({ items: [1, 2] }, "items"), [1, 2]);
  assert.deepEqual(extractRows({ by_ip: [{ key: "10.0.0.1" }] }, "by_ip"), [{ key: "10.0.0.1" }]);
  assert.deepEqual(extractRows([1, 2], "items"), [1, 2]);
  assert.deepEqual(extractRows(null, "items"), []);
  assert.deepEqual(extractRows({ items: "x" }, "items"), []);
  assert.deepEqual(extractRows({ items: [1] }, ""), [1]);
});

test("formatUnit 按单位格式化", () => {
  assert.match(formatUnit(1536, "bytes"), /1\.5/);
  assert.match(formatUnit(1500000, "rate"), /1\.5/);
  assert.match(formatUnit(1500, "count"), /1\.5/);
  assert.equal(formatUnit(12.345, "percent"), "12.35 %");
  assert.equal(formatUnit(0.1234, "ratio"), "12.34 %");
  assert.equal(formatUnit(42, ""), "42");
  assert.equal(formatUnit("abc", ""), "abc");
  assert.equal(formatUnit(null, "bytes"), "-");
  assert.equal(formatUnit(undefined, "rate"), "-");
});