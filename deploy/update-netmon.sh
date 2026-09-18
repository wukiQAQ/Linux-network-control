#!/usr/bin/env bash
# =============================================================================
# netmon 一键更新脚本（Linux）
#
#   停旧进程 -> 备份旧二进制 -> 安装新二进制 -> 启动 -> 校验版本与能力
#
# 用法：
#   ./update-netmon.sh                          # 用当前目录的 netmon-linux 更新
#   ./update-netmon.sh -b ./netmon-linux -c ~/config.toml
#   ./update-netmon.sh --rollback               # 回滚到最近一次备份
#   ./update-netmon.sh --dry-run                # 只打印将要执行的动作
#
# 常用参数：
#   -b, --binary <路径>   新二进制（默认 ./netmon-linux）
#   -d, --dir <目录>      安装目录（默认 $HOME）
#   -c, --config <路径>   配置文件（默认 $HOME/config.toml）
#   -s, --service <名字>  systemd 服务名（默认 netmon；存在则用 systemd 管理）
#   -p, --port <端口>     健康检查端口（默认 8080）
#   -n, --no-start        只替换不启动
#       --rollback        回滚到最近一次备份并启动
#       --dry-run         演练：只打印动作
# =============================================================================
set -euo pipefail

BIN_NAME="netmon-linux"
BINARY="./${BIN_NAME}"
INSTALL_DIR="${HOME}"
CONFIG="${HOME}/config.toml"
SERVICE="netmon"
PORT="8080"
DO_START=1
DRY_RUN=0
DO_ROLLBACK=0
KEEP_BACKUPS=3

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_yel=$'\033[33m'; c_dim=$'\033[2m'; c_off=$'\033[0m'
log()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$c_grn" "$c_off" "$*"; }
warn() { printf '%s[警告]%s %s\n' "$c_yel" "$c_off" "$*"; }
die()  { printf '%s[错误]%s %s\n' "$c_red" "$c_off" "$*" >&2; exit 1; }
run()  { if [ "$DRY_RUN" = "1" ]; then printf '%s[dry-run]%s %s\n' "$c_dim" "$c_off" "$*"; else eval "$@"; fi; }

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    -b|--binary)  BINARY="${2:?缺少参数}"; shift 2 ;;
    -d|--dir)     INSTALL_DIR="${2:?缺少参数}"; shift 2 ;;
    -c|--config)  CONFIG="${2:?缺少参数}"; shift 2 ;;
    -s|--service) SERVICE="${2:?缺少参数}"; shift 2 ;;
    -p|--port)    PORT="${2:?缺少参数}"; shift 2 ;;
    -n|--no-start) DO_START=0; shift ;;
    --rollback)   DO_ROLLBACK=1; shift ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *)            die "未知参数：$1（用 -h 查看用法）" ;;
  esac
done

TARGET="${INSTALL_DIR%/}/${BIN_NAME}"

# ---------- 工具函数 ----------
have() { command -v "$1" >/dev/null 2>&1; }

# 是否由 systemd 管理（单元存在且 systemctl 可用）
use_systemd() {
  have systemctl || return 1
  if [ -f "/etc/systemd/system/${SERVICE}.service" ] || [ -f "/usr/lib/systemd/system/${SERVICE}.service" ]; then
    return 0
  fi
  systemctl list-unit-files "${SERVICE}.service" 2>/dev/null | grep -q "${SERVICE}.service" && return 0
  return 1
}

is_elf() {
  head -c 4 "$1" 2>/dev/null | od -An -tx1 | tr -d ' \n' | grep -qi '^7f454c46'
}

any_pid_on_port() {
  if have ss; then
    ss -ltnp 2>/dev/null | grep -q ":${PORT} "
  elif have netstat; then
    netstat -ltnp 2>/dev/null | grep -q ":${PORT} "
  else
    return 1
  fi
}

wait_port_free() {
  local i
  for i in $(seq 1 20); do
    any_pid_on_port || return 0
    sleep 0.5
  done
  return 1
}

# 只结束"监听指定端口的 netmon"进程；绝不使用 pkill -f（它会匹配到本脚本/ssh 自身的命令行）
kill_port_owner() {
  local pids pid exe target_real
  target_real="$(readlink -f "$TARGET" 2>/dev/null || echo "$TARGET")"
  pids=""
  if have ss; then
    pids="$(ss -ltnp 2>/dev/null | awk -v p=":${PORT}" 'index($4, p) > 0' | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u || true)"
  fi
  if [ -z "$pids" ] && have fuser; then
    pids="$(fuser -n tcp "${PORT}" 2>/dev/null | tr -s ' ' '
' | grep -E '^[0-9]+$' || true)"
  fi
  if [ -z "$pids" ]; then
    warn "端口 ${PORT} 上没找到监听进程"
    return 0
  fi
  for pid in $pids; do
    [ "$pid" = "$$" ] && continue
    exe=""
    [ -r "/proc/$pid/exe" ] && exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
    if [ "$exe" = "$target_real" ] || [ "$(basename "${exe:-}")" = "$BIN_NAME" ]; then
      log "  结束进程 pid=$pid (${exe:-未知})"
      run "kill $pid 2>/dev/null || true"
    else
      warn "端口 ${PORT} 被其他程序占用（pid=$pid exe=${exe:-未知}），未做处理；请改用其他端口"
      return 1
    fi
  done
  return 0
}

# 兜底：按进程名精确匹配（-x 不会匹配到 bash/ssh/本脚本）
kill_by_name() {
  local pid
  pgrep -x "$BIN_NAME" 2>/dev/null | while read -r pid; do
    [ "$pid" = "$$" ] && continue
    log "  结束进程 pid=$pid（按进程名匹配）"
    run "kill $pid 2>/dev/null || true"
  done || true
  return 0
}

latest_backup() {
  ls -1t "${INSTALL_DIR%/}/${BIN_NAME}.bak."* 2>/dev/null | head -n 1 || true
}

rotate_backups() {
  local list
  list="$(ls -1t "${INSTALL_DIR%/}/${BIN_NAME}.bak."* 2>/dev/null || true)"
  [ -z "$list" ] && return 0
  echo "$list" | tail -n "+$((KEEP_BACKUPS + 1))" | while read -r old; do
    if [ -n "$old" ]; then run "rm -f '${old}'"; fi
  done || true
  return 0
}

# ---------- 回滚模式 ----------
if [ "$DO_ROLLBACK" = "1" ]; then
  BACKUP="$(latest_backup)"
  [ -z "$BACKUP" ] && die "没有找到备份文件（${INSTALL_DIR%/}/${BIN_NAME}.bak.*）"
  step "回滚到备份：${BACKUP}"
  if use_systemd && sudo -n true 2>/dev/null; then
    run "sudo systemctl stop '${SERVICE}'"
  else
    kill_port_owner || true
    kill_by_name
  fi
  sleep 1
  run "cp -a '${BACKUP}' '${TARGET}'"
  run "chmod 0755 '${TARGET}'"
  if [ "$DO_START" = "1" ]; then
    if use_systemd; then run "sudo systemctl start '${SERVICE}'"; else run "nohup '${TARGET}' -config '${CONFIG}' >> '${INSTALL_DIR%/}/netmon.log' 2>&1 &"; fi
  fi
  log "回滚完成，可执行 '${TARGET}' -h 或查看日志 ${INSTALL_DIR%/}/netmon.log"
  exit 0
fi

# ---------- 更新模式 ----------
step "开始更新 netmon"
log "  新二进制 ：${BINARY}"
log "  安装位置 ：${TARGET}"
log "  配置文件 ：${CONFIG}"
log "  服务单元 ：${SERVICE}$(use_systemd && echo '（使用 systemd）' || echo '（未发现 systemd 单元，使用进程方式）')"

# 1) 校验新二进制
[ -f "$BINARY" ] || die "找不到新二进制：${BINARY}"
[ -s "$BINARY" ] || die "新二进制为空：${BINARY}"
if ! is_elf "$BINARY"; then
  die "新文件不是 Linux 可执行文件（不是 ELF）——确认上传的是 netmon-linux，而不是 .exe 或被截断"
fi
log "  $(ls -lh "$BINARY" | awk '{print "新二进制大小："$5}')"

# 2) 停旧进程
step "停止旧进程"
set +e
if use_systemd && sudo -n true 2>/dev/null; then
  # 有 systemd 单元且 sudo 免密：交给 systemd 管理最干净
  run "sudo systemctl stop '${SERVICE}'"
else
  if use_systemd; then
    warn "sudo 需要密码（非交互 ssh 无法输入）：改为直接结束监听 ${PORT} 端口的进程"
    warn "想用 systemd 管理，请用 ssh -t 连接后再执行本脚本，或配置 sudo 免密"
  fi
  kill_port_owner || true
  kill_by_name || true
fi
set -e
sleep 1 || true
if [ "$DRY_RUN" != "1" ]; then
  if ! wait_port_free; then
    warn "端口 ${PORT} 仍被占用，请检查：sudo ss -ltnp | grep ':${PORT}'"
    warn "若不是 netmon，请改用其他端口（-p 或修改 config.toml 的 [api] listen）"
  fi
fi

# 3) 备份旧二进制
step "备份旧二进制"
if [ -f "$TARGET" ]; then
  BACKUP="${TARGET}.bak.$(date +%Y%m%d-%H%M%S)"
  run "cp -a '${TARGET}' '${BACKUP}'"
  log "  备份：${BACKUP}"
  rotate_backups || true
else
  warn "安装位置没有旧二进制，跳过备份"
fi

# 4) 安装新二进制
step "安装新二进制"
run "install -m 0755 '${BINARY}' '${TARGET}'"

# 5) 启动
if [ "$DO_START" = "1" ]; then
  step "启动"
  if use_systemd; then
    run "sudo systemctl start '${SERVICE}'"
  else
    run "cd '${INSTALL_DIR%/}' && nohup './${BIN_NAME}' -config '${CONFIG}' >> '${INSTALL_DIR%/}/netmon.log' 2>&1 &"
  fi
  [ "$DRY_RUN" = "1" ] || sleep 2
else
  warn "按参数要求未启动，可手动执行：${TARGET} -config ${CONFIG}"
fi

# 6) 校验版本与能力
if [ "$DO_START" = "1" ] && [ "$DRY_RUN" != "1" ]; then
  step "校验服务"
  ok=0
  for i in $(seq 1 10); do
    if have curl; then
      body="$(curl -fsS --max-time 3 "http://127.0.0.1:${PORT}/api/v1/traffic/now" 2>/dev/null || true)"
    else
      body="$(wget -qO- --timeout=3 "http://127.0.0.1:${PORT}/api/v1/traffic/now" 2>/dev/null || true)"
    fi
    if [ -n "$body" ]; then
      ver="$(printf '%s' "$body" | grep -o '"version":"[^"]*"' | head -n1 | cut -d'"' -f4 || true)"
      feat="$(printf '%s' "$body" | grep -o '"features":\[[^]]*\]' | head -n1 || true)"
      log "  ${c_grn}服务已响应${c_off}  版本=${ver:-未上报}"
      log "  能力：${feat:-未上报}"
      if [ -n "$ver" ]; then ok=1; fi
      break
    fi
    sleep 1
  done
  if [ "$ok" = "1" ]; then
    if ! use_systemd; then
      log "${c_dim}提示：本次以进程方式启动（未使用 systemd）。如需开机自启，请参考使用指南配置 netmon.service${c_off}"
    fi
    step "更新完成 ✅"
  else
    warn "服务已启动但未获取到版本信息（可能是旧版二进制，或端口不是 ${PORT}）"
    warn "查看日志：tail -n 30 ${INSTALL_DIR%/}/netmon.log"
  fi
  log "${c_dim}回滚命令：${0} --rollback${c_off}"
else
  step "更新完成（未启动或演练模式）"
fi