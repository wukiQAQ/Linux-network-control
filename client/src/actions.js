// 运维动作（白名单动作）相关的纯逻辑与文案：分类、命令预览、参数预校验、结果格式化。

export const CATEGORY_META = {
  system: { label: "系统", icon: "🖥️", nav: "system", hint: "查看系统资源、进程与登录情况" },
  network: { label: "网络", icon: "🌐", nav: "network", hint: "查看网卡、路由、端口与连接统计" },
  service: { label: "服务", icon: "🧩", nav: "service", hint: "查看、启动、重启 systemd 服务" },
  logs: { label: "系统日志", icon: "📜", nav: "syslog", hint: "查看系统与服务日志尾部" },
  files: { label: "文件", icon: "📁", nav: "files", hint: "查看目录内容与文件尾部" },
};

// 左侧导航中属于"运维动作"的入口
export const ACTION_NAV_KEYS = Object.values(CATEGORY_META).map((m) => m.nav);

// 导航 key -> 动作分类 key
export function categoryOfNav(nav) {
  const hit = Object.entries(CATEGORY_META).find(([, m]) => m.nav === nav);
  return hit ? hit[0] : "";
}

// 按分类分组（顺序固定，空分类也会返回，便于界面显示"该分类暂无动作"）
export function groupActions(items) {
  const list = Array.isArray(items) ? items : [];
  return Object.entries(CATEGORY_META).map(([key, meta]) => ({
    key,
    label: meta.label,
    icon: meta.icon,
    nav: meta.nav,
    hint: meta.hint,
    items: list.filter((a) => a && a.category === key),
  }));
}

// 把参数代入命令模板
export function resolveCommand(template, params) {
  let out = String(template === undefined || template === null ? "" : template);
  const p = params && typeof params === "object" ? params : {};
  Object.keys(p).forEach((k) => {
    out = out.split("{" + k + "}").join(String(p[k] === undefined || p[k] === null ? "" : p[k]));
  });
  return out;
}

// 生成"将在 Linux 上执行"的命令预览
export function previewCommands(action, params) {
  const cmds = action && Array.isArray(action.commands) ? action.commands : [];
  return cmds.map((c) => resolveCommand(c, params));
}

// 与服务端一致的参数校验（服务端仍会再校验一次，这里只是提前提示）
export function validateActionParams(action, params) {
  const p = params && typeof params === "object" ? params : {};
  const defs = action && Array.isArray(action.params) ? action.params : [];
  const out = {};
  for (const d of defs) {
    const v = String(p[d.name] === undefined || p[d.name] === null ? "" : p[d.name]).trim();
    const label = d.label || d.name;
    if (!v) {
      if (d.required) return { ok: false, error: "请填写：" + label, params: {} };
      out[d.name] = "";
      continue;
    }
    if (d.pattern) {
      let re = null;
      try {
        re = new RegExp(d.pattern);
      } catch {
        re = null;
      }
      if (re && !re.test(v)) {
        return { ok: false, error: label + "格式不正确" + (d.hint ? "：" + d.hint : ""), params: {} };
      }
    }
    if (v.includes("..")) return { ok: false, error: label + "不允许包含 ..", params: {} };
    out[d.name] = v;
  }
  return { ok: true, error: "", params: out };
}

// 执行结果文本（stdout + stderr）
export function formatActionResult(res) {
  const r = res && typeof res === "object" ? res : {};
  const parts = [];
  if (r.stdout) parts.push(String(r.stdout).replace(/\s+$/, ""));
  if (r.stderr) parts.push("---- stderr ----\n" + String(r.stderr).replace(/\s+$/, ""));
  if (r.truncated) parts.push("（输出已截断）");
  if (!parts.length) parts.push("（无输出）");
  return parts.join("\n");
}

// 执行结果摘要
export function actionResultSummary(res) {
  const r = res && typeof res === "object" ? res : {};
  const code = Number.isFinite(Number(r.exit_code)) ? Number(r.exit_code) : -1;
  const ms = Number.isFinite(Number(r.duration_ms)) ? Number(r.duration_ms) : 0;
  return (code === 0 ? "执行成功" : "执行结束（退出码 " + code + "）") + " · 用时 " + ms + " ms";
}

// 历史记录的一行文案
export function historyText(entry) {
  const e = entry && typeof entry === "object" ? entry : {};
  const code = Number.isFinite(Number(e.exit_code)) ? Number(e.exit_code) : -1;
  return (e.title || e.action_id || "动作") + " · exit " + code;
}