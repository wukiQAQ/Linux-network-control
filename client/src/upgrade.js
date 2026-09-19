// 服务端"一键升级"相关的纯逻辑：能力判断、表单校验、结果文案。
// 抽成独立模块是为了能写单元测试（App.vue 只负责调用与渲染）。

// normalizeSha256 统一小写并去掉首尾空白，避免大小写导致校验失败。
export function normalizeSha256(input) {
  return String(input === null || input === undefined ? "" : input).trim().toLowerCase();
}

// serverUpgradeEnabled 判断服务端是否开启了自升级（需服务端配置 [update] enabled）。
export function serverUpgradeEnabled(status) {
  return Boolean(status && status.enabled === true);
}

// upgradeDisabledHint 返回"一键升级"按钮不可用时的可操作提示。
export function upgradeDisabledHint(connected, status, version) {
  if (!connected) return "未连接";
  if (serverUpgradeEnabled(status)) return "需提供新版本的下载地址与 SHA256";
  const v = typeof version === "string" && version ? version : "";
  return v
    ? "服务端为 V" + v + "，未启用自升级（需 0.16.0+，并在 config.toml 打开 [update] enabled）"
    : "服务端未上报自升级能力（需 0.16.0+），请更新 Linux 端 netmon";
}

// validateUpgradeInput 校验升级表单；ok 为 true 时返回规整后的 url 与 sha256。
export function validateUpgradeInput(url, sha) {
  const u = String(url === null || url === undefined ? "" : url).trim();
  const s = normalizeSha256(sha);
  if (!u) return { ok: false, error: "请填写新版本的下载地址" };
  if (!/^https?:\/\//i.test(u)) return { ok: false, error: "下载地址必须是 http/https" };
  if (!/^[0-9a-f]{64}$/.test(s)) return { ok: false, error: "SHA256 必须是 64 位十六进制（0-9a-f）" };
  return { ok: true, error: "", url: u, sha256: s };
}

// upgradeResultText 把服务端返回的升级结果转成面板文案。
export function upgradeResultText(res) {
  const r = res || {};
  if (!r.applied) return "";
  return [
    "程序已原子替换，旧版本已备份（~/netmon-backups/）",
    "SHA256：" + (r.sha256 || "-"),
    r.restart_scheduled
      ? "服务端将在约 1 秒后重启，客户端会自动重连"
      : "未安排重启，请手动执行 systemctl restart netmon",
  ].join("\n");
}

// lastUpgradeSummary 汇总服务端返回的"最近一次升级"状态，用于弹窗里的提示行。
export function lastUpgradeSummary(status) {
  if (!status) return "";
  const parts = [];
  if (status.last_at) parts.push("最近一次升级：" + status.last_at);
  if (status.last_sha256) parts.push("SHA256 " + String(status.last_sha256).slice(0, 12) + "…");
  if (status.applied) parts.push("已生效");
  if (status.last_error) parts.push("失败：" + status.last_error);
  return parts.join("　");
}