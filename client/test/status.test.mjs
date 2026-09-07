import { test } from "node:test";
import assert from "node:assert/strict";
import { statusView } from "../src/status.js";

test("未连接：灰色 idle 状态", () => {
  assert.deepEqual(statusView(false, "", false), { cls: "idle", text: "未连接" });
});

test("已连接：绿色 ok 并显示连接名称", () => {
  assert.deepEqual(statusView(true, "CentOS", false), {
    cls: "ok",
    text: "已连接 CentOS",
  });
});

test("连接失败：红色 bad 状态", () => {
  assert.deepEqual(statusView(false, "CentOS", true), {
    cls: "bad",
    text: "连接失败",
  });
});