// 运维动作（白名单动作）相关的纯逻辑与文案：分类、命令预览、参数预校验、结果格式化。

// 左侧导航里的统一「Linux 指令」入口：所有分类的指令集中在这一个任务选项里
export const COMMANDS_NAV = { key: "cmds", icon: "⌨️", label: "Linux 指令" };

// 分类元数据（键顺序即界面展示顺序）
export const CATEGORY_META = {
  system: { label: "系统", icon: "🖥️", hint: "查看系统资源、进程与登录情况" },
  network: { label: "网络", icon: "🌐", hint: "查看网卡、路由、端口与连接统计" },
  service: { label: "服务", icon: "🧩", hint: "查看、启动、重启 systemd 服务" },
  logs: { label: "系统日志", icon: "📜", hint: "查看系统与服务日志尾部" },
  files: { label: "文件", icon: "📁", hint: "查看目录内容与文件尾部" },
};

// 按分类分组（顺序固定，空分类也会返回，便于界面显示"该分类暂无动作"）
export function groupActions(items) {
  const list = Array.isArray(items) ? items : [];
  return Object.entries(CATEGORY_META).map(([key, meta]) => ({
    key,
    label: meta.label,
    icon: meta.icon,
    hint: meta.hint,
    items: list.filter((a) => a && a.category === key),
  }));
}

// 动作里的命令模板（原样返回，含 {参数} 占位符）：用于"全部指令"一览
export function commandTemplates(action) {
  const cmds = action && Array.isArray(action.commands) ? action.commands : [];
  return cmds.map((c) => String(c));
}

// 指令总览里的动作条目数
export function countActions(groups) {
  const list = Array.isArray(groups) ? groups : [];
  return list.reduce((n, g) => n + (g && Array.isArray(g.items) ? g.items.length : 0), 0);
}

// 指令总览里真正会在 Linux 上执行的命令条数（一条动作可能包含多条命令）
export function countCommands(groups) {
  const list = Array.isArray(groups) ? groups : [];
  return list.reduce((n, g) => {
    const items = g && Array.isArray(g.items) ? g.items : [];
    return n + items.reduce((m, a) => m + commandTemplates(a).length, 0);
  }, 0);
}

// 把全部分类的指令汇总成一段可复制的文本（按分类分节，空分类跳过）
export function commandsDigest(groups) {
  const list = Array.isArray(groups) ? groups : [];
  const out = [];
  for (const g of list) {
    const items = g && Array.isArray(g.items) ? g.items : [];
    if (!items.length) continue;
    out.push("# " + String((g && g.label) || (g && g.key) || ""));
    for (const a of items) {
      out.push("# " + String((a && a.title) || (a && a.id) || ""));
      for (const c of commandTemplates(a)) out.push(c);
    }
  }
  return out.join("\n");
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