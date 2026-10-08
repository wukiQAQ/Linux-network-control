// 数据来源模块（client/src/localsource.js）的单元测试
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MODE_STORE_KEY,
  SOURCE_MODES,
  checkStartLocalAnalysis,
  loadModePref,
  localBase,
  localDataDir,
  localEngineArgs,
  modeHint,
  modeLabel,
  modeSupport,
  normalizeMode,
} from "../src/localsource.js";

test("SOURCE_MODES 为远程/本机/离线三选一", () => {
  assert.deepEqual(SOURCE_MODES, ["remote", "local", "offline"]);
  assert.equal(MODE_STORE_KEY, "netmon.source.v1");
});

test("normalizeMode 容错非法值", () => {
  assert.equal(normalizeMode("remote"), "remote");
  assert.equal(normalizeMode("local"), "local");
  assert.equal(normalizeMode("offline"), "offline");
  // undefined / null / 空串 / 非法值 / 其它类型 一律回落到 remote
  assert.equal(normalizeMode(undefined), "remote");
  assert.equal(normalizeMode(null), "remote");
  assert.equal(normalizeMode(""), "remote");
  assert.equal(normalizeMode("REMOTE"), "remote");
  assert.equal(normalizeMode("远程"), "remote");
  assert.equal(normalizeMode(0), "remote");
  assert.equal(normalizeMode({ mode: "local" }), "remote");
});

test("modeLabel 输出中文来源名", () => {
  assert.equal(modeLabel("remote"), "远程 Linux");
  assert.equal(modeLabel("local"), "本机 Windows（实时抓包）");
  assert.equal(modeLabel("offline"), "离线 pcap 文件");
  // 非法值按 remote 处理
  assert.equal(modeLabel("bad"), "远程 Linux");
});

test("localBase 拼出本机引擎地址", () => {
  assert.equal(localBase("127.0.0.1:18080"), "http://127.0.0.1:18080");
  assert.equal(localBase(" 127.0.0.1:9527 "), "http://127.0.0.1:9527");
  // 没有 listen 时只返回协议前缀，由上层校验拦住
  assert.equal(localBase(""), "http://");
  assert.equal(localBase(undefined), "http://");
});

test("localDataDir 优先 LOCALAPPDATA", () => {
  const dir = localDataDir({ LOCALAPPDATA: "C:\\Users\\me\\AppData\\Local", USERPROFILE: "C:\\Users\\me" });
  assert.equal(dir, "C:\\Users\\me\\AppData\\Local\\MeTD\\local-data");
});

test("localDataDir 无 LOCALAPPDATA 时退回 USERPROFILE", () => {
  const dir = localDataDir({ USERPROFILE: "C:\\Users\\me" });
  assert.equal(dir, "C:\\Users\\me\\AppData\\Local\\MeTD\\local-data");
});

test("localDataDir 两者都没有返回空串", () => {
  assert.equal(localDataDir({}), "");
  assert.equal(localDataDir(null), "");
  // 仅空白的变量视为缺失，避免拼出 "\MeTD\local-data" 这种相对路径
  assert.equal(localDataDir({ LOCALAPPDATA: "   ", USERPROFILE: "" }), "");
});

test("localDataDir 不产生重复分隔符", () => {
  assert.equal(
    localDataDir({ LOCALAPPDATA: "C:\\Users\\me\\AppData\\Local\\" }),
    "C:\\Users\\me\\AppData\\Local\\MeTD\\local-data",
  );
  assert.equal(
    localDataDir({ LOCALAPPDATA: "C:/Users/me/AppData/Local/" }),
    "C:/Users/me/AppData/Local\\MeTD\\local-data",
  );
});

test("localEngineArgs remote 不启动本机进程", () => {
  assert.equal(localEngineArgs({ mode: "remote" }), null);
  // 缺 mode 时按 remote 处理
  assert.equal(localEngineArgs({}), null);
  assert.equal(localEngineArgs(undefined), null);
});

test("localEngineArgs offline 返回 netmon 参数数组", () => {
  const args = localEngineArgs({
    mode: "offline",
    pcapPath: "D:\\pcaps\\demo.pcap",
    dataDir: "C:\\Users\\me\\AppData\\Local\\MeTD\\local-data",
    listen: "127.0.0.1:18080",
  });
  assert.deepEqual(args, [
    "-replay-file",
    "D:\\pcaps\\demo.pcap",
    "-data-dir",
    "C:\\Users\\me\\AppData\\Local\\MeTD\\local-data",
    "-listen",
    "127.0.0.1:18080",
  ]);
});

test("localEngineArgs offline 缺 pcap 抛错", () => {
  assert.throws(
    () => localEngineArgs({ mode: "offline", pcapPath: "  ", dataDir: "C:\\d", listen: "127.0.0.1:1" }),
    /离线 pcap 模式必须选择 pcap 文件/,
  );
});

test("localEngineArgs offline 缺数据目录抛错", () => {
  assert.throws(
    () => localEngineArgs({ mode: "offline", pcapPath: "D:\\a.pcap", dataDir: "", listen: "127.0.0.1:1" }),
    /本机引擎缺少数据目录/,
  );
});

test("localEngineArgs offline 缺监听地址抛错", () => {
  assert.throws(
    () => localEngineArgs({ mode: "offline", pcapPath: "D:\\a.pcap", dataDir: "C:\\d", listen: "" }),
    /本机引擎缺少监听地址/,
  );
});

test("localEngineArgs local 抛未实现错误", () => {
  assert.throws(
    () => localEngineArgs({ mode: "local", dataDir: "C:\\d", listen: "127.0.0.1:1" }),
    /尚未实现/,
  );
});

test("modeHint 逐来源给出说明", () => {
  assert.equal(modeHint("remote"), "");
  assert.match(modeHint("local"), /尚未实现/);
  assert.match(modeHint("local"), /管理员权限/);
  assert.match(modeHint("offline"), /无需管理员权限/);
  assert.match(modeHint("offline"), /文件/);
});

test("modeSupport remote 恒可用", () => {
  assert.deepEqual(modeSupport("remote", {}, "linux"), { ok: true, hint: "" });
  assert.deepEqual(modeSupport("remote", null, undefined), { ok: true, hint: "" });
});

test("modeSupport offline 依赖采集端能力", () => {
  // 老采集端未声明 capture.offline，提示升级并带上服务端版本号
  const noCap = modeSupport("offline", { version: "0.22.0", features: ["capture.dump"] }, "linux");
  assert.equal(noCap.ok, false);
  assert.match(noCap.hint, /离线回放/);
  assert.match(noCap.hint, /0\.22\.0/);
  // 未上报版本时也有可读提示
  const noVer = modeSupport("offline", {}, "linux");
  assert.equal(noVer.ok, false);
  assert.match(noVer.hint, /未上报版本/);
  // 采集端 0.23.4 声明了能力，可用
  const ok = modeSupport("offline", { version: "0.23.4", features: ["capture.offline"] }, "linux");
  assert.deepEqual(ok, { ok: true, hint: "" });
  // 远程来源的能力与 engineOs 无关（本机引擎跑在 Windows，但 offline 不依赖它）
  assert.deepEqual(modeSupport("offline", { features: ["capture.offline"] }, "windows"), { ok: true, hint: "" });
});

test("modeSupport local 本版本恒不可用且提示尚未实现", () => {
  // 采集端故意不声明 source.local
  const unsupported = modeSupport("local", { version: "0.23.4", features: ["capture.dump", "capture.offline"] }, "windows");
  assert.equal(unsupported.ok, false);
  assert.match(unsupported.hint, /尚未实现/);
  // 即便采集端将来声明了 source.local，非 windows 中枢仍不可用，且提示同样含「尚未实现」
  const notWindows = modeSupport("local", { features: ["source.local"] }, "linux");
  assert.equal(notWindows.ok, false);
  assert.match(notWindows.hint, /尚未实现/);
  // 唯一理论上的可用分支（保留给后续实现）
  assert.deepEqual(modeSupport("local", { features: ["source.local"] }, "windows"), { ok: true, hint: "" });
});

test("checkStartLocalAnalysis 只校验来源与 pcap 路径", () => {
  // 远程 / 本机来源不适用
  assert.equal(checkStartLocalAnalysis({ mode: "remote", pcapPath: "D:\\a.pcap" }).ok, false);
  assert.match(checkStartLocalAnalysis({ mode: "remote" }).reason, /不是「离线 pcap 文件」/);
  // 离线但没填 pcap
  assert.equal(checkStartLocalAnalysis({ mode: "offline", pcapPath: "   " }).ok, false);
  assert.match(checkStartLocalAnalysis({ mode: "offline" }).reason, /必须选择 pcap 文件/);
  // 离线 + 有 pcap：即便此刻还没连上任何采集端（now 里还没有 features）也要放行，
  // 否则第一次使用时会因为"未上报能力"而永远点不动（能力判定推后到连上之后）
  assert.deepEqual(checkStartLocalAnalysis({ mode: "offline", pcapPath: " D:\\a.pcap " }), { ok: true, reason: "" });
});

test("loadModePref 容错读取偏好", () => {
  assert.deepEqual(loadModePref(null), { mode: "remote", pcapPath: "", dataDir: "" });
  assert.deepEqual(loadModePref(undefined), { mode: "remote", pcapPath: "", dataDir: "" });
  assert.deepEqual(loadModePref("offline"), { mode: "remote", pcapPath: "", dataDir: "" });
  assert.deepEqual(loadModePref({ mode: "offline" }), { mode: "offline", pcapPath: "", dataDir: "" });
  assert.deepEqual(
    loadModePref({ mode: "offline", pcapPath: "D:\\a.pcap", dataDir: "C:\\d" }),
    { mode: "offline", pcapPath: "D:\\a.pcap", dataDir: "C:\\d" },
  );
  // 非法 mode 回落 remote，字段一律转成字符串
  assert.deepEqual(loadModePref({ mode: "nope", pcapPath: 7 }), { mode: "remote", pcapPath: "7", dataDir: "" });
});
