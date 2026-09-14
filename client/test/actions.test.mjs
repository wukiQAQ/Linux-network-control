import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ACTION_NAV_KEYS,
  actionResultSummary,
  categoryOfNav,
  formatActionResult,
  groupActions,
  historyText,
  previewCommands,
  resolveCommand,
  validateActionParams,
} from "../src/actions.js";

const disk = { id: "system.disk", category: "system", title: "磁盘使用率", commands: ["df -h"], params: [] };
const restart = {
  id: "service.restart",
  category: "service",
  title: "重启服务",
  danger: true,
  commands: ["systemctl restart {service}", "systemctl is-active {service}"],
  params: [{ name: "service", label: "服务名", required: true, pattern: "^[A-Za-z0-9_.@-]{1,64}$", hint: "只允许字母数字" }],
};

test("categoryOfNav 与导航分类映射", () => {
  assert.equal(categoryOfNav("system"), "system");
  assert.equal(categoryOfNav("syslog"), "logs");
  assert.equal(categoryOfNav("dash"), "");
  assert.ok(ACTION_NAV_KEYS.includes("files"));
});

test("groupActions 按分类分组且顺序固定", () => {
  const groups = groupActions([disk, restart]);
  assert.deepEqual(groups.map((g) => g.key), ["system", "network", "service", "logs", "files"]);
  assert.equal(groups[0].items.length, 1);
  assert.equal(groups[2].items[0].id, "service.restart");
  assert.equal(groups[1].items.length, 0);
  assert.deepEqual(groupActions(null).length, 5);
});

test("resolveCommand 与 previewCommands 替换参数", () => {
  assert.equal(resolveCommand("df -h", {}), "df -h");
  assert.equal(resolveCommand("systemctl restart {service}", { service: "sshd" }), "systemctl restart sshd");
  assert.deepEqual(previewCommands(restart, { service: "nginx" }), [
    "systemctl restart nginx",
    "systemctl is-active nginx",
  ]);
  assert.deepEqual(previewCommands(null, {}), []);
});

test("validateActionParams 校验必填与格式", () => {
  assert.equal(validateActionParams(disk, {}).ok, true);
  const missing = validateActionParams(restart, {});
  assert.equal(missing.ok, false);
  assert.ok(missing.error.includes("请填写"));
  const bad = validateActionParams(restart, { service: "ssh; rm -rf /" });
  assert.equal(bad.ok, false);
  const dotdot = validateActionParams(
    { params: [{ name: "path", label: "路径", required: true }] },
    { path: "/var/../etc" },
  );
  assert.equal(dotdot.ok, false);
  const ok = validateActionParams(restart, { service: " sshd " });
  assert.equal(ok.ok, true);
  assert.equal(ok.params.service, "sshd");
});

test("formatActionResult 组合输出", () => {
  assert.equal(formatActionResult({ stdout: "hello\n", exit_code: 0 }), "hello");
  const both = formatActionResult({ stdout: "a", stderr: "b" });
  assert.ok(both.includes("a"));
  assert.ok(both.includes("---- stderr ----"));
  assert.ok(both.includes("b"));
  assert.ok(formatActionResult({ truncated: true }).includes("截断"));
  assert.ok(formatActionResult({}).includes("无输出"));
});

test("actionResultSummary 与 historyText", () => {
  assert.ok(actionResultSummary({ exit_code: 0, duration_ms: 12 }).includes("执行成功"));
  assert.ok(actionResultSummary({ exit_code: 3, duration_ms: 5 }).includes("退出码 3"));
  assert.ok(historyText({ title: "磁盘使用率", exit_code: 0 }).includes("磁盘使用率"));
});