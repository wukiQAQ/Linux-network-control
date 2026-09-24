#!/usr/bin/env bash
# 采集性能基线：给 PACKET_MMAP 之类的采集层优化做"改前 / 改后"对比。
#
# 只读脚本：不修改配置、不重启服务，只采样系统计数与 netmon 的监控接口。
#
# 用法：
#   sudo bash bench-capture.sh [-i 网卡] [-p 端口] [-s 秒数] [-t 令牌]
# 例：
#   sudo bash bench-capture.sh -i ens33 -p 8080 -s 30
#
# 输出：接口 rx 增量、内核侧丢包、netmon 上报的收包/丢包/丢包率、平均 bps/pps、netmon 进程平均 CPU% 与 RSS。
# 注意：脚本用 set -uo pipefail（故意不启用 -e），任何一步取不到数据都只提示，不中断采样。

set -uo pipefail

IFACE=""
PORT=8080
SECONDS_TO_RUN=30
TOKEN=""

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

while getopts ":i:p:s:t:h" opt; do
  case "$opt" in
    i) IFACE="$OPTARG" ;;
    p) PORT="$OPTARG" ;;
    s) SECONDS_TO_RUN="$OPTARG" ;;
    t) TOKEN="$OPTARG" ;;
    h) usage ;;
    *) usage ;;
  esac
done

if [ -z "$IFACE" ]; then
  IFACE=$(ip -br a 2>/dev/null | awk '$1 != "lo" && $2 == "UP" {print $1; exit}')
  if [ -z "$IFACE" ]; then
    IFACE=$(ip -br a 2>/dev/null | awk '$1 != "lo" {print $1; exit}')
  fi
fi
if [ -z "$IFACE" ]; then
  echo "找不到可用网卡，请用 -i 指定（ip -br a 查看）" >&2
  exit 1
fi

API="http://127.0.0.1:${PORT}/api/v1/traffic/now"
CURL_AUTH=()
if [ -n "$TOKEN" ]; then
  CURL_AUTH=(-H "Authorization: Bearer ${TOKEN}")
fi

sx() { cat "/sys/class/net/$IFACE/statistics/$1" 2>/dev/null || echo 0; }

fetch_api() {
  if command -v curl >/dev/null 2>&1; then
    curl -s --max-time 2 "${CURL_AUTH[@]}" "$API" 2>/dev/null
  else
    wget -qO- --timeout=2 --header "Authorization: Bearer ${TOKEN}" "$API" 2>/dev/null
  fi
}

pick() { printf '%s' "$1" | sed -n "s/.*\"$2\":\([-0-9.]*\).*/\1/p" | head -n1; }

NETMON_PID=$(pgrep -f 'netmon-linux|/netmon($| )' 2>/dev/null | head -n1)
if [ -z "$NETMON_PID" ]; then
  NETMON_PID=$(pgrep -x netmon 2>/dev/null | head -n1)
fi

echo "== 采集性能基线 =="
echo "网卡        : $IFACE"
echo "接口地址    : $(ip -br a show "$IFACE" 2>/dev/null | awk '{print $3}')"
echo "监控接口    : $API"
echo "netmon 进程 : ${NETMON_PID:-未找到（CPU/RSS 将不采样）}"
echo "采样时长    : ${SECONDS_TO_RUN}s"
echo "内核 readers/绑定等配置请用: grep -E 'readers|cpu_pin|filter' config.toml"
echo

rx0=$(sx rx_packets); rb0=$(sx rx_bytes); rd0=$(sx rx_dropped); re0=$(sx rx_errors)
sum_bps=0; sum_pps=0; n=0; sum_cpu=0; peak_rss=0
last_recv=0; last_drop=0; first_json=""

for i in $(seq 1 "$SECONDS_TO_RUN"); do
  json=$(fetch_api)
  if [ -n "$json" ]; then
    bps=$(pick "$json" bps); pps=$(pick "$json" pps)
    last_recv=$(pick "$json" recv_packets); last_drop=$(pick "$json" drop_packets)
    [ -z "$first_json" ] && first_json="$json"
    sum_bps=$(awk -v a="$sum_bps" -v b="${bps:-0}" 'BEGIN{printf "%.3f", a+b}')
    sum_pps=$(awk -v a="$sum_pps" -v b="${pps:-0}" 'BEGIN{printf "%.3f", a+b}')
    n=$((n+1))
  fi
  if [ -n "$NETMON_PID" ] && [ -r "/proc/$NETMON_PID/stat" ]; then
    cpu=$(ps -o %cpu= -p "$NETMON_PID" 2>/dev/null | tr -d ' ')
    rss=$(ps -o rss= -p "$NETMON_PID" 2>/dev/null | tr -d ' ')
    [ -n "$cpu" ] && sum_cpu=$(awk -v a="$sum_cpu" -v b="$cpu" 'BEGIN{printf "%.3f", a+b}')
    if [ -n "$rss" ] && [ "$rss" -gt "$peak_rss" ] 2>/dev/null; then peak_rss="$rss"; fi
  fi
  sleep 1
done

rx1=$(sx rx_packets); rb1=$(sx rx_bytes); rd1=$(sx rx_dropped); re1=$(sx rx_errors)

echo "---- 网卡计数（$IFACE）----"
awk -v v0="$rx0" -v v1="$rx1" 'BEGIN{printf "rx_packets 增量 : %d\n", v1-v0}'
awk -v v0="$rb0" -v v1="$rb1" 'BEGIN{printf "rx_bytes   增量 : %d\n", v1-v0}'
awk -v v0="$rd0" -v v1="$rd1" 'BEGIN{printf "rx_dropped 增量 : %d （内核/驱动侧丢包，比 netmon 自己报的更靠前）\n", v1-v0}'
awk -v v0="$re0" -v v1="$re1" 'BEGIN{printf "rx_errors  增量 : %d\n", v1-v0}'
echo
echo "---- netmon 自身上报 ----"
echo "累计收包        : ${last_recv:-n/a}"
echo "累计丢包        : ${last_drop:-n/a}"
if [ "$n" -gt 0 ]; then
  awk -v b="$sum_bps" -v p="$sum_pps" -v c="$n" 'BEGIN{printf "平均 bps        : %.0f\n平均 pps        : %.0f\n", b/c, p/c}'
  if [ -n "$NETMON_PID" ]; then
    awk -v c="$sum_cpu" -v n="$n" 'BEGIN{printf "平均 CPU 占用   : %.1f %% （单核百分比，>100 表示多核并行）\n", c/n}'
    awk -v r="$peak_rss" 'BEGIN{printf "RSS 峰值        : %.1f MB\n", r/1024}'
  fi
  dr=$(printf '%s' "$first_json" | sed -n 's/.*"drop_rate":\([-0-9.eE]*\).*/\1/p' | head -n1)
  [ -n "$dr" ] && echo "丢包率          : $dr"
else
  echo "（采样期间取不到 /api/v1/traffic/now，检查端口、令牌与防火墙）"
fi
echo
echo "提示：改前 / 改后各跑一次同样的网卡与时长，对比 rx_dropped 增量、平均 CPU% 与平均 bps 即可判断优化收益。"