import { test } from "node:test";
import assert from "node:assert/strict";
import { captureSupport, hasFeature, serverVersion } from "../src/capability.js";

test("hasFeature 判断服务端能力", () => {
  assert.equal(hasFeature({ features: ["capture.dump"] }, "capture.dump"), true);
  assert.equal(hasFeature({ features: ["alerts"] }, "capture.dump"), false);
  assert.equal(hasFeature({}, "capture.dump"), false);
  assert.equal(hasFeature(null, "capture.dump"), false);
  assert.equal(hasFeature({ features: "capture.dump" }, "capture.dump"), false);
});

test("serverVersion 容错", () => {
  assert.equal(serverVersion({ version: "0.7.1" }), "0.7.1");
  assert.equal(serverVersion({}), "");
  assert.equal(serverVersion(null), "");
});

test("服务端支持抓包时不显示提示", () => {
  const s = captureSupport({ version: "0.7.1", features: ["capture.dump"] });
  assert.equal(s.ok, true);
  assert.equal(s.hint, "");
});

test("旧版服务端给出可操作提示", () => {
  const s = captureSupport({ version: "0.5.4", features: ["alerts"] });
  assert.equal(s.ok, false);
  assert.ok(s.hint.includes("0.5.4"));
  assert.ok(s.hint.includes("更新 Linux 端"));
});

test("未上报版本的服务端也给出提示", () => {
  const s = captureSupport({});
  assert.equal(s.ok, false);
  assert.ok(s.hint.includes("未上报"));
});