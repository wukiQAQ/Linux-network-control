import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CAPTURE_SECONDS_DEFAULT,
  captureErrorText,
  CAPTURE_SECONDS_MAX,
  CAPTURE_SECONDS_MIN,
  captureDoneText,
  captureFileName,
  captureProgressText,
  clampCaptureSeconds,
} from "../src/capture.js";

test("clampCaptureSeconds 夹取范围", () => {
  assert.equal(clampCaptureSeconds(0), CAPTURE_SECONDS_DEFAULT);
  assert.equal(clampCaptureSeconds(-5), CAPTURE_SECONDS_DEFAULT);
  assert.equal(clampCaptureSeconds("abc"), CAPTURE_SECONDS_DEFAULT);
  assert.equal(clampCaptureSeconds(0.4), CAPTURE_SECONDS_DEFAULT);
  assert.equal(clampCaptureSeconds(1), CAPTURE_SECONDS_MIN);
  assert.equal(clampCaptureSeconds(15), 15);
  assert.equal(clampCaptureSeconds(9999), CAPTURE_SECONDS_MAX);
});

test("captureProgressText 展示包数与剩余时间", () => {
  const status = { packets: 3, bytes: 1200, started_at: new Date().toISOString() };
  const text = captureProgressText(status, 15);
  assert.match(text, /已捕获 3 包/);
  assert.match(text, /1200 字节/);
  assert.match(text, /剩余约 1[45] 秒/);
});

test("captureProgressText 容错空状态", () => {
  const text = captureProgressText(null, 0);
  assert.match(text, /已捕获 0 包/);
  assert.match(text, /剩余约 15 秒/);
});

test("captureDoneText 与 captureFileName", () => {
  assert.equal(captureDoneText({ packets: 10, bytes: 2048 }), "抓包完成：10 包 / 2048 字节");
  assert.equal(captureFileName({ file: "capture-1.pcap" }), "capture-1.pcap");
  assert.equal(captureFileName(null), "");
});

test("captureErrorText 把错误翻译成可操作提示", () => {
  assert.ok(captureErrorText("HTTP 404: 404 page not found").includes("更新到 V0.7.0"));
  assert.ok(captureErrorText("HTTP 409: 已有导出在进行中").includes("已有抓包任务"));
  assert.ok(captureErrorText("HTTP 503: 抓包导出未启用").includes("未启用抓包导出"));
  assert.ok(captureErrorText("抓包超时，请稍后重试").includes("超时"));
  assert.equal(captureErrorText(""), "未知错误");
  assert.equal(captureErrorText("其他错误"), "其他错误");
});