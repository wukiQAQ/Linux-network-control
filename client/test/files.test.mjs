import { test } from "node:test";
import assert from "node:assert/strict";
import { entryIcon, fileSizeText, joinPath, parentPath, sortEntries } from "../src/files.js";

test("parentPath / joinPath", () => {
  assert.equal(parentPath("/var/log"), "/var");
  assert.equal(parentPath("/var"), "/");
  assert.equal(parentPath("/"), "");
  assert.equal(joinPath("/var", "log"), "/var/log");
  assert.equal(joinPath("/", "etc"), "/etc");
  assert.equal(joinPath("/var/", "/log"), "/var/log");
});

test("sortEntries 目录优先", () => {
  const out = sortEntries([{ name: "b.log" }, { name: "a", is_dir: true }, { name: "A.txt" }]);
  assert.equal(out[0].name, "a");
  assert.deepEqual(out.slice(1).map((e) => e.name), ["A.txt", "b.log"]);
  assert.deepEqual(sortEntries(null), []);
});

test("fileSizeText 单位换算", () => {
  assert.equal(fileSizeText(512), "512 B");
  assert.equal(fileSizeText(2048), "2.0 KB");
  assert.equal(fileSizeText(5 * 1024 * 1024), "5.0 MB");
  assert.equal(fileSizeText(-1), "-");
  assert.equal(fileSizeText("x"), "-");
});

test("entryIcon", () => {
  assert.equal(entryIcon({ is_dir: true }), "📁");
  assert.equal(entryIcon({}), "📄");
});