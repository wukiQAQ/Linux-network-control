import { test } from "node:test";
import assert from "node:assert/strict";
import {
  findProfile,
  lastUsedText,
  makeProfile,
  normalizeProfiles,
  profileKey,
  profileName,
  removeProfile,
  sortProfiles,
  upsertProfile,
} from "../src/profiles.js";

test("profileKey 归一化地址（去空白与结尾斜杠）", () => {
  assert.equal(profileKey(" http://192.168.1.10:8080/ "), "http://192.168.1.10:8080");
  assert.equal(profileKey("host:8080///"), "host:8080");
  assert.equal(profileKey(null), "");
});

test("profileName 优先名称，其次地址", () => {
  assert.equal(profileName({ name: "CentOS", base: "a:1" }), "CentOS");
  assert.equal(profileName({ name: "  ", base: "a:1" }), "a:1");
  assert.equal(profileName({}), "未命名连接");
});

test("upsertProfile 新增连接并记录最近使用时间", () => {
  const list = upsertProfile([], { base: "http://10.0.0.2:8080", name: "机架 A", lastUsed: 100 });
  assert.equal(list.length, 1);
  assert.equal(list[0].base, "http://10.0.0.2:8080");
  assert.equal(list[0].name, "机架 A");
  assert.equal(list[0].lastUsed, 100);
});

test("upsertProfile 以地址去重并保留最新信息", () => {
  const first = upsertProfile([], { base: "http://10.0.0.2:8080/", name: "旧名", token: "t1", lastUsed: 1 });
  const second = upsertProfile(first, { base: "http://10.0.0.2:8080", name: "新名", token: "t2", lastUsed: 5 });
  assert.equal(second.length, 1);
  assert.equal(second[0].name, "新名");
  assert.equal(second[0].token, "t2");
  assert.equal(second[0].lastUsed, 5);
});

test("upsertProfile 忽略无地址记录且不修改入参", () => {
  const list = [{ name: "A", base: "a:1", token: "", lastUsed: 1 }];
  const after = upsertProfile(list, { base: "", name: "空" });
  assert.equal(after.length, 1);
  assert.equal(list.length, 1);
  assert.equal(list[0].name, "A");
});

test("sortProfiles 按最近使用倒序", () => {
  const list = sortProfiles([
    { name: "B", base: "b", lastUsed: 10 },
    { name: "A", base: "a", lastUsed: 99 },
    { name: "C", base: "c", lastUsed: 0 },
  ]);
  assert.deepEqual(list.map((p) => p.name), ["A", "B", "C"]);
});

test("normalizeProfiles 丢弃脏数据并按地址去重", () => {
  const list = normalizeProfiles([
    { base: "a:1", name: "旧", lastUsed: 1 },
    { base: "a:1/", name: "新", lastUsed: 2 },
    { name: "没有地址" },
    null,
    "bad",
  ]);
  assert.equal(list.length, 1);
  assert.equal(list[0].name, "新");
  assert.deepEqual(normalizeProfiles("not-array"), []);
});

test("removeProfile 与 findProfile 按地址操作", () => {
  const list = normalizeProfiles([
    { base: "a:1", name: "A", lastUsed: 1 },
    { base: "b:2", name: "B", lastUsed: 2 },
  ]);
  assert.equal(findProfile(list, "a:1/").name, "A");
  assert.equal(findProfile(list, "zz"), null);
  const after = removeProfile(list, "a:1");
  assert.equal(after.length, 1);
  assert.equal(after[0].base, "b:2");
});

test("makeProfile 容错非法字段", () => {
  const p = makeProfile(null, { name: 123, token: null, lastUsed: "abc" });
  assert.equal(p.base, "");
  assert.equal(p.name, "");
  assert.equal(p.token, "");
  assert.equal(p.lastUsed, 0);
});

test("lastUsedText 输出可读文案", () => {
  const now = 1000000000000;
  assert.equal(lastUsedText(0, now), "未连接过");
  assert.equal(lastUsedText(now - 30 * 1000, now), "刚刚连接");
  assert.equal(lastUsedText(now - 5 * 60 * 1000, now), "5 分钟前连接");
  assert.equal(lastUsedText(now - 3 * 3600 * 1000, now), "3 小时前连接");
  assert.equal(lastUsedText(now - 2 * 86400 * 1000, now), "2 天前连接");
});