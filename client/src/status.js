// 连接状态展示逻辑（纯函数，便于单元测试）
export function statusView(connected, label, hasError) {
  if (connected) return { cls: "ok", text: "已连接 " + label };
  if (hasError) return { cls: "bad", text: "连接失败" };
  return { cls: "idle", text: "未连接" };
}

// 连接操作的提示消息（成功/失败/校验警告），便于 toast 展示与单元测试
export function connectMessage(kind, label, error) {
  const name = label || "服务器";
  if (kind === "ok") return { type: "ok", text: "连接成功：" + name };
  if (kind === "err") {
    return { type: "err", text: "连接失败：" + (error || "未知错误") };
  }
  return { type: "err", text: error || "请检查输入内容" };
}