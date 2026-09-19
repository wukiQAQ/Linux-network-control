import assert from "node:assert/strict";
import test from "node:test";

import {
  lastUpgradeSummary,
  normalizeSha256,
  serverUpgradeEnabled,
  upgradeDisabledHint,
  upgradeResultText,
  validateUpgradeInput,
} from "../src/upgrade.js";

const SHA = "a".repeat(64);

test("normalizeSha256 统一小写并去空白", () => {
  assert.equal(normalizeSha256("  ABC123  "), "abc123");
  assert.equal(normalizeSha256(null), "");
  assert.equal(normalizeSha256(undefined), "");
});

test("serverUpgradeEnabled 只认 enabled=true", () => {
  assert.equal(serverUpgradeEnabled({ enabled: true }), true);
  assert.equal(serverUpgradeEnabled({ enabled: false }), false);
  assert.equal(serverUpgradeEnabled(null), false);
});

test("upgradeDisabledHint 给出可操作提示", () => {
  assert.equal(upgradeDisabledHint(false, { enabled: true }), "未连接");
  assert.equal(upgradeDisabledHint(true, { enabled: true }), "需提供新版本的下载地址与 SHA256");
  assert.match(upgradeDisabledHint(true, { enabled: false }, "0.15.0"), /0\.15\.0/);
  assert.match(upgradeDisabledHint(true, { enabled: false }), /未上报自升级能力/);
});

test("validateUpgradeInput 校验地址与 SHA256", () => {
  assert.equal(validateUpgradeInput("", SHA).ok, false);
  assert.match(validateUpgradeInput("", SHA).error, /下载地址/);
  assert.equal(validateUpgradeInput("ftp://x/y", SHA).ok, false);
  assert.equal(validateUpgradeInput("https://x/y", "abc").ok, false);
  assert.match(validateUpgradeInput("https://x/y", "abc").error, /64 位十六进制/);
  const ok = validateUpgradeInput("  https://x/y  ", SHA.toUpperCase());
  assert.equal(ok.ok, true);
  assert.equal(ok.url, "https://x/y");
  assert.equal(ok.sha256, SHA);
});

test("upgradeResultText 只在 applied 时输出", () => {
  assert.equal(upgradeResultText(null), "");
  assert.equal(upgradeResultText({ applied: false }), "");
  const t = upgradeResultText({ applied: true, sha256: SHA, restart_scheduled: true });
  assert.match(t, /SHA256/);
  assert.match(t, /自动重连/);
  assert.match(upgradeResultText({ applied: true, restart_scheduled: false }), /systemctl restart netmon/);
});

test("lastUpgradeSummary 汇总最近一次升级", () => {
  assert.equal(lastUpgradeSummary(null), "");
  assert.equal(lastUpgradeSummary({}), "");
  const s = lastUpgradeSummary({ last_at: "2026-09-19T00:00:00Z", last_sha256: SHA, applied: true });
  assert.match(s, /2026-09-19T00:00:00Z/);
  assert.match(s, /已生效/);
  assert.match(lastUpgradeSummary({ last_error: "白名单拒绝" }), /白名单拒绝/);
});