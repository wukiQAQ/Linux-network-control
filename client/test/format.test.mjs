import { test } from "node:test";
import assert from "node:assert/strict";
import {
  normalizeBaseUrl,
  fmtRate,
  fmtBytes,
  fmtPps,
  fmtNum,
  fmtDuration,
  protoLabel,
} from "../src/format.js";

test("normalizeBaseUrl 补全协议并去掉尾斜杠", () => {
  assert.deepEqual(normalizeBaseUrl("192.168.1.100:8080"), {
    ok: true,
    url: "http://192.168.1.100:8080",
  });
  assert.deepEqual(normalizeBaseUrl("https://linux.example.com:8443/"), {
    ok: true,
    url: "https://linux.example.com:8443",
  });
  assert.deepEqual(normalizeBaseUrl(" http://10.0.0.2 "), {
    ok: true,
    url: "http://10.0.0.2",
  });
});

test("normalizeBaseUrl 拒绝空值与非法地址", () => {
  assert.equal(normalizeBaseUrl("").ok, false);
  assert.equal(normalizeBaseUrl("   ").ok, false);
  assert.equal(normalizeBaseUrl("ht!tp://bad").ok, false);
});

test("速率/字节/数字格式化", () => {
  assert.equal(fmtRate(0), "0 b/s");
  assert.equal(fmtRate(1500000), "1.5 Mb/s");
  assert.equal(fmtRate(999), "999 b/s");
  assert.equal(fmtBytes(0), "0 B");
  assert.equal(fmtBytes(2048), "2.05 KB");
  assert.equal(fmtBytes(1048576), "1.05 MB");
  assert.equal(fmtPps(1234), "1.23k pps");
  assert.equal(fmtNum(8500), "8.5k");
});

test("非有限数值返回占位符", () => {
  assert.equal(fmtRate(Number.NaN), "-");
  assert.equal(fmtBytes(undefined), "-");
});

test("fmtDuration 与 protoLabel", () => {
  assert.equal(fmtDuration(45), "45 秒");
  assert.equal(fmtDuration(125), "2 分 5 秒");
  assert.equal(fmtDuration(3600 + 61), "1 小时 1 分");
  assert.equal(protoLabel("tcp"), "TCP");
  assert.equal(protoLabel("UDP"), "UDP");
  assert.equal(protoLabel("icmp"), "ICMP");
  assert.equal(protoLabel("47"), "47");
});