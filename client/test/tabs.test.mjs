import { test } from "node:test";
import assert from "node:assert/strict";
import {
  closeTabById,
  createTab,
  emptySnapshot,
  newTabId,
  nextActiveId,
  normalizeTabs,
} from "../src/tabs.js";

test("新建标签页带有空快照", () => {
  const t = createTab("id-1", "工作区 2");
  assert.equal(t.id, "id-1");
  assert.equal(t.title, "工作区 2");
  assert.deepEqual(t.snapshot, emptySnapshot());
});

test("normalizeTabs 容错并补齐字段", () => {
  const list = normalizeTabs([{ title: "A" }, null, { id: "x", snapshot: { total: 5 } }]);
  assert.equal(list.length, 3);
  assert.equal(list[1].title, "工作区 2");
  assert.equal(list[2].snapshot.total, 5);
  assert.equal(list[2].snapshot.page, 1);
  const fallback = normalizeTabs([]);
  assert.equal(fallback.length, 1);
  assert.equal(fallback[0].title, "工作区 1");
});

test("关闭标签页至少保留一个", () => {
  const a = createTab("a");
  const b = createTab("b");
  const after = closeTabById([a, b], "a");
  assert.equal(after.length, 1);
  assert.equal(after[0].id, "b");
  const single = closeTabById([a], "a");
  assert.equal(single.length, 1);
});

test("关闭当前页后切换到剩余页", () => {
  const list = [createTab("a"), createTab("b")];
  assert.equal(nextActiveId(list, "a", "a"), "a");
  assert.equal(nextActiveId(list, "b", "b"), "a");
  assert.ok(newTabId().startsWith("ws-"));
});