import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_SETTINGS,
  clampOpacity,
  loadSettings,
  normalizeSettings,
  saveSettings,
} from "../src/settings.js";

test("clampOpacity 限制在 0.55~1", () => {
  assert.equal(clampOpacity(0.3), 0.55);
  assert.equal(clampOpacity(0.8), 0.8);
  assert.equal(clampOpacity(1.5), 1);
  assert.equal(clampOpacity("abc"), 1);
});

test("normalizeSettings 补齐默认值并校验字段", () => {
  const s = normalizeSettings({ theme: "light", opacity: 0.2 });
  assert.equal(s.theme, "light");
  assert.equal(s.opacity, 0.55);
  assert.equal(s.closeToTray, true);
  assert.equal(s.autostart, false);
});

test("load/save 使用 localStorage 往返", () => {
  const store = new Map();
  const storage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
  };
  saveSettings(storage, { theme: "light", opacity: 0.9 });
  const loaded = loadSettings(storage);
  assert.equal(loaded.theme, "light");
  assert.equal(loaded.opacity, 0.9);
});

test("loadSettings 容错损坏数据", () => {
  const storage = { getItem: () => "{bad json", setItem: () => {} };
  const s = loadSettings(storage);
  assert.deepEqual(s, DEFAULT_SETTINGS);
});