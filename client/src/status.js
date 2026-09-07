// 连接状态展示逻辑（纯函数，便于单元测试）
export function statusView(connected, label, hasError) {
  if (connected) return { cls: "ok", text: "已连接 " + label };
  if (hasError) return { cls: "bad", text: "连接失败" };
  return { cls: "idle", text: "未连接" };
}