import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canOpenLocalFile,
  entryIcon,
  fileExt,
  fileSizeText,
  isDangerousExt,
  joinPath,
  parentPath,
  sortEntries,
} from "../src/files.js";

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
  assert.equal(fileSizeText(2048), "2 KB");
  assert.equal(fileSizeText(5 * 1024 * 1024), "5 MB");
  assert.equal(fileSizeText(-1), "-");
  assert.equal(fileSizeText("x"), "-");
});

test("entryIcon", () => {
  assert.equal(entryIcon({ is_dir: true }), "📁");
  assert.equal(entryIcon({}), "📄");
});

test("fileExt 取最后一个点后的扩展名", () => {
  assert.equal(fileExt("app.log"), "log");
  assert.equal(fileExt("APP.LOG"), "log");
  assert.equal(fileExt("report.pdf.exe"), "exe");
  assert.equal(fileExt("/var/log/syslog.1.gz"), "gz");
  // 无扩展名 / 只有点 / 点开头：都视为无扩展名
  assert.equal(fileExt("syslog"), "");
  assert.equal(fileExt("syslog."), "");
  assert.equal(fileExt(".bashrc"), "");
  assert.equal(fileExt(""), "");
  assert.equal(fileExt(null), "");
});

// 这是代码审查 H4 的回归：下载后不得自动打开可执行/脚本类文件。
test("isDangerousExt / canOpenLocalFile 拦截可执行类", () => {
  for (const n of [
    "update.exe",
    "run.bat",
    "x.cmd",
    "a.com",
    "s.scr",
    "p.pif",
    "m.msi",
    "c.cpl",
    "p.ps1",
    "v.vbs",
    "j.js",
    "w.wsf",
    "h.hta",
    "l.lnk",
    "r.reg",
    "jar.jar",
    "u.url",
    "c.chm",
    "i.inf",
    "report.pdf.exe", // 伪装成 pdf，仍按最后一个扩展名判定
    "RUN.BAT",
  ]) {
    assert.equal(isDangerousExt(n), true, n + " 应被判为可执行类");
    assert.equal(canOpenLocalFile(n), false, n + " 不应允许打开");
  }
  for (const n of ["access.log", "syslog", "config.toml", "dump.pcap", "notes.txt", "nginx.conf"]) {
    assert.equal(isDangerousExt(n), false, n + " 不应被判为可执行类");
    assert.equal(canOpenLocalFile(n), true, n + " 应允许打开");
  }
});