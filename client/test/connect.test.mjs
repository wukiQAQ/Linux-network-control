import { test } from "node:test";
import assert from "node:assert/strict";
import {
  cancelAttempt,
  cancelNotice,
  connectButtonLabel,
  isCurrentAttempt,
  newAttemptToken,
  startAttempt,
} from "../src/connect.js";

test("startAttempt 每次递增序号", () => {
  const token = newAttemptToken();
  const a = startAttempt(token);
  const b = startAttempt(token);
  assert.equal(a, 1);
  assert.equal(b, 2);
});

test("cancelAttempt 让旧尝试立即失效", () => {
  const token = newAttemptToken();
  const seq = startAttempt(token);
  assert.equal(isCurrentAttempt(token, seq), true);
  cancelAttempt(token);
  assert.equal(isCurrentAttempt(token, seq), false);
  const next = startAttempt(token);
  assert.equal(isCurrentAttempt(token, next), true);
  assert.equal(isCurrentAttempt(token, seq), false);
});

test("连接中重复取消不会让新尝试失效", () => {
  const token = newAttemptToken();
  const seq = startAttempt(token);
  cancelAttempt(token);
  cancelAttempt(token);
  const fresh = startAttempt(token);
  assert.equal(isCurrentAttempt(token, fresh), true);
  assert.equal(isCurrentAttempt(token, seq), false);
});

test("非法 token 与序号容错", () => {
  assert.equal(startAttempt(null), 0);
  assert.equal(cancelAttempt(undefined), 0);
  assert.equal(isCurrentAttempt(null, 0), false);
  assert.equal(isCurrentAttempt(newAttemptToken(), 99), false);
});

test("连接按钮文案随状态变化", () => {
  assert.equal(connectButtonLabel(false, false), "连接");
  assert.equal(connectButtonLabel(false, true), "重新连接");
  assert.equal(connectButtonLabel(true, false), "取消连接");
  assert.equal(connectButtonLabel(true, true), "取消连接");
});

test("取消提示文案", () => {
  assert.deepEqual(cancelNotice("CentOS"), { type: "err", text: "已取消连接：CentOS" });
  assert.deepEqual(cancelNotice(""), { type: "err", text: "已取消连接" });
});