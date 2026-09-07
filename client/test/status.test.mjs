import { test } from "node:test";
import assert from "node:assert/strict";
import { connectMessage, statusView } from "../src/status.js";

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

test("connectMessage：成功提示", () => {
  assert.deepEqual(connectMessage("ok", "CentOS"), {
    type: "ok",
    text: "连接成功：CentOS",
  });
});

test("connectMessage：失败提示携带原因", () => {
  const msg = connectMessage("err", "CentOS", "HTTP 401");
  assert.equal(msg.type, "err");
  assert.equal(msg.text, "连接失败：HTTP 401");
});

test("connectMessage：校验警告", () => {
  const msg = connectMessage("warn", "", "请输入服务器地址");
  assert.equal(msg.type, "err");
  assert.equal(msg.text, "请输入服务器地址");
});