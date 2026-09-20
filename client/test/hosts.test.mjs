import { test } from "node:test";
import assert from "node:assert/strict";
import { featureBadges, fleetSummary, hostMetrics, hostStatus, hostVersion, sortHosts } from "../src/hosts.js";

test("hostStatus 在线/离线", () => {
  assert.equal(hostStatus({ ok: true }).key, "online");
  assert.equal(hostStatus({ ok: false, error: "超时" }).key, "offline");
  assert.equal(hostStatus(null).key, "offline");
});

test("hostVersion 与 featureBadges", () => {
  assert.equal(hostVersion({ now: { version: "0.11.0" } }), "V0.11.0");
  assert.equal(hostVersion({}), "未上报");
  assert.deepEqual(featureBadges({ now: { features: ["stream", "files", "actions.run"] } }), ["运维", "文件", "实时"]);
  assert.deepEqual(featureBadges({ now: { features: ["capture.filter"] } }), ["过滤"]);
  assert.deepEqual(featureBadges({}), []);
});

test("sortHosts 在线优先、再按名称", () => {
  const rows = [
    { name: "b", probe: { ok: false } },
    { name: "c", probe: { ok: true } },
    { name: "a", probe: { ok: true } },
  ];
  assert.deepEqual(sortHosts(rows).map((r) => r.name), ["a", "c", "b"]);
  assert.deepEqual(sortHosts(null), []);
});

test("fleetSummary 统计", () => {
  const s = fleetSummary([{ probe: { ok: true } }, { probe: { ok: false } }, {}]);
  assert.deepEqual(s, { total: 3, online: 1, offline: 2 });
});

test("hostMetrics 摘要文本", () => {
  const text = hostMetrics({ now: { bps: 2500000, pps: 1200, conns: 12, uptime_s: 3900 } });
  assert.ok(text.includes("2.5 Mb/s"));
  assert.ok(text.includes("1200 pps"));
  assert.ok(text.includes("1h5m"));
  assert.equal(hostMetrics({}), "-");
});