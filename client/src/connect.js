// 连接过程的可取消令牌（纯逻辑，便于单元测试）。
// 每次发起连接都会拿到一个递增序号；取消时序号再 +1，旧流程的后续结果全部作废，
// 从而实现"连接中随时可以取消"，而不需要打断底层已经在飞的请求。

export function newAttemptToken() {
  return { seq: 0 };
}

// 开始一次连接尝试，返回本次尝试的序号。
export function startAttempt(token) {
  if (!token || typeof token !== "object") return 0;
  token.seq += 1;
  return token.seq;
}

// 取消当前尝试（序号 +1 即让旧序号失效）。
export function cancelAttempt(token) {
  if (!token || typeof token !== "object") return 0;
  token.seq += 1;
  return token.seq;
}

// 判断某个序号是否仍是当前尝试。
export function isCurrentAttempt(token, seq) {
  return Boolean(token) && typeof token === "object" && token.seq === seq;
}

// 连接按钮文案：连接中时按钮变成"取消连接"。
export function connectButtonLabel(connecting, connected) {
  if (connecting) return "取消连接";
  return connected ? "重新连接" : "连接";
}

// 取消后的提示文案。
export function cancelNotice(label) {
  return { type: "err", text: "已取消连接" + (label ? "：" + label : "") };
}