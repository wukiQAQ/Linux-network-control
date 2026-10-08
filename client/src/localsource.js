// 数据来源（三选一）纯逻辑模块：
//   remote  —— 连接远程 Linux 上的 netmon（原有能力，默认）
//   local   —— 在本机 Windows 上实时抓包（本版本尚未实现，只保留入口与提示）
//   offline —— 用本机 netmon.exe 回放一个 pcap 文件（-replay-file），界面里的数字来自文件而非当前网络
// 这里不引入 DOM / Tauri，全部是可用 node:test 直接验证的纯函数。
import { hasFeature, serverVersion } from "./capability.js";

// 三种数据来源的枚举，顺序即界面上的展示顺序
export const SOURCE_MODES = ["remote", "local", "offline"];

// 数据来源偏好的持久化键（与 profiles / settings 的 v1 键风格一致）
export const MODE_STORE_KEY = "netmon.source.v1";

// 采集端提供「离线回放」能力的最低版本，仅用于提示文案
const OFFLINE_SINCE = "V0.23.0+";

// 容错归一化：合法枚举原样返回，其余（undefined / null / 空串 / 非法值）一律回落到 remote
export function normalizeMode(mode) {
  return SOURCE_MODES.includes(mode) ? mode : "remote";
}

// 界面上的来源名称
export function modeLabel(mode) {
  switch (normalizeMode(mode)) {
    case "local":
      return "本机 Windows（实时抓包）";
    case "offline":
      return "离线 pcap 文件";
    default:
      return "远程 Linux";
  }
}

// 本机引擎的 HTTP 地址：listen 形如 127.0.0.1:18080
export function localBase(listen) {
  const s = typeof listen === "string" ? listen.trim() : "";
  return "http://" + s;
}

// 目录拼接前去掉结尾的路径分隔符，避免出现 `C:\a\\MeTD` 这种重复分隔符
function trimTrailingSep(path) {
  return String(path).trim().replace(/[\\/]+$/, "");
}

// 本机引擎的数据目录：优先 %LOCALAPPDATA%\MeTD\local-data，
// 退回 %USERPROFILE%\AppData\Local\MeTD\local-data，两者都没有时返回空串
// （env 由调用方注入，方便测试，Rust 侧也用同一套规则拼路径）。
export function localDataDir(env) {
  const e = env && typeof env === "object" ? env : {};
  const localAppData = typeof e.LOCALAPPDATA === "string" ? e.LOCALAPPDATA.trim() : "";
  if (localAppData) return trimTrailingSep(localAppData) + "\\MeTD\\local-data";
  const userProfile = typeof e.USERPROFILE === "string" ? e.USERPROFILE.trim() : "";
  if (userProfile) return trimTrailingSep(userProfile) + "\\AppData\\Local\\MeTD\\local-data";
  return "";
}

// 组装启动本机 netmon.exe 的参数：
//   remote  → null（不启动本机进程）
//   offline → ["-replay-file", pcap, "-data-dir", dir, "-listen", listen]
//   local   → 抛错（本版本未实现）
// 参数与 Rust 侧 sidecar 启动逻辑保持一致，缺参数时在这里就报出可读的中文错误。
export function localEngineArgs(opts) {
  const o = opts && typeof opts === "object" ? opts : {};
  const mode = normalizeMode(o.mode);
  if (mode === "remote") return null;
  if (mode === "local") {
    throw new Error(
      "本机实时抓包尚未实现（采集端 Windows 抓包开发中），请改用「离线 pcap 文件」或连接远程 Linux",
    );
  }
  const pcapPath = typeof o.pcapPath === "string" ? o.pcapPath.trim() : "";
  if (!pcapPath) throw new Error("离线 pcap 模式必须选择 pcap 文件");
  const dataDir = typeof o.dataDir === "string" ? o.dataDir.trim() : "";
  if (!dataDir) throw new Error("本机引擎缺少数据目录");
  const listen = typeof o.listen === "string" ? o.listen.trim() : "";
  if (!listen) throw new Error("本机引擎缺少监听地址");
  return ["-replay-file", pcapPath, "-data-dir", dataDir, "-listen", listen];
}

// 「启动本机分析」按钮的前置校验（纯函数，便于测试）：
// 只校验来源模式与 pcap 路径——本机引擎的能力要等它起来、连上之后才知道，
// 启动前拿采集端上报的 features 去拦，会因为首次使用时 `now` 还是空的而"永远点不动"。
export function checkStartLocalAnalysis(opts) {
  const o = opts && typeof opts === "object" ? opts : {};
  if (normalizeMode(o.mode) !== "offline") {
    return { ok: false, reason: "当前数据来源不是「离线 pcap 文件」" };
  }
  const pcapPath = typeof o.pcapPath === "string" ? o.pcapPath.trim() : "";
  if (!pcapPath) return { ok: false, reason: "离线 pcap 模式必须选择 pcap 文件" };
  return { ok: true, reason: "" };
}

// 每种来源的补充说明（界面直接显示，remote 没有额外说明）
export function modeHint(mode) {
  switch (normalizeMode(mode)) {
    case "local":
      return "需管理员权限，且只能看到本机网卡的流量；本版本尚未实现（采集端 Windows 抓包开发中）";
    case "offline":
      return "无需管理员权限，只分析所选 pcap 文件；界面上的数字来自文件，而不是当前网络";
    default:
      return "";
  }
}

// 判断某种来源在当前环境下是否可用，返回 {ok, hint}：
//   remote  —— 恒可用（沿用原有连接流程）
//   offline —— 需要采集端声明 capture.offline（采集端 0.23.4 起提供）
//   local   —— 需要采集端声明 source.local 且运行在 windows；本版本采集端故意不声明该能力，
//              因此这里恒为不可用，提示里必须写明「尚未实现」
export function modeSupport(mode, now, engineOs) {
  const m = normalizeMode(mode);
  if (m === "remote") return { ok: true, hint: "" };
  if (m === "offline") {
    if (hasFeature(now, "capture.offline")) return { ok: true, hint: "" };
    const v = serverVersion(now);
    return {
      ok: false,
      hint: v
        ? `服务端为 V${v}，不支持离线回放（需 ${OFFLINE_SINCE}），请更新 Linux 端 netmon`
        : `服务端未上报版本/能力，离线回放不可用（需 ${OFFLINE_SINCE}），请更新 Linux 端 netmon`,
    };
  }
  if (!hasFeature(now, "source.local")) {
    return {
      ok: false,
      hint: "本机实时抓包尚未实现（采集端 Windows 抓包开发中），请改用「离线 pcap 文件」或连接远程 Linux",
    };
  }
  if (String(engineOs || "") !== "windows") {
    return { ok: false, hint: "当前采集中枢不是 Windows 本机，实时抓包尚未实现" };
  }
  return { ok: true, hint: "" };
}

// 字符串字段容错：只接受字符串，其它一律当成空串
function asString(v) {
  return v === undefined || v === null ? "" : String(v);
}

// 读取数据来源偏好（raw 为已解析的对象或 null），任何脏数据都回落到 remote
export function loadModePref(raw) {
  const r = raw && typeof raw === "object" ? raw : {};
  return {
    mode: normalizeMode(r.mode),
    pcapPath: asString(r.pcapPath),
    dataDir: asString(r.dataDir),
  };
}
