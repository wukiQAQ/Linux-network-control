<template>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th>协议</th><th>源地址</th><th>源端口</th><th>目的地址</th><th>目的端口</th>
          <th class="num">包数</th><th class="num">字节</th><th>开始</th><th>结束</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td colspan="9" class="empty">{{ loading ? "加载中…" : "暂无会话记录（空闲会话需约 30 秒后落库）" }}</td>
        </tr>
        <tr v-for="(row, i) in rows" :key="i">
          <td><span class="proto" :class="row.proto">{{ protoLabel(row.proto) }}</span></td>
          <td>{{ row.src_ip }}</td>
          <td class="num">{{ row.src_port || "-" }}</td>
          <td>{{ row.dst_ip }}</td>
          <td class="num">{{ row.dst_port || "-" }}</td>
          <td class="num">{{ row.packets }}</td>
          <td class="num">{{ fmtBytes(row.bytes) }}</td>
          <td>{{ fmtTs(row.start) }}</td>
          <td>{{ fmtTs(row.end) }}</td>
        </tr>
      </tbody>
    </table>
    <div class="pager">
      <span class="muted">共 {{ total }} 条 · 第 {{ page }} 页</span>
      <span>
        <button class="btn" :disabled="page <= 1" @click="$emit('page', page - 1)">上一页</button>
        <button class="btn" :disabled="rows.length < pageSize" @click="$emit('page', page + 1)">下一页</button>
      </span>
    </div>
  </div>
</template>

<script setup>
import { fmtBytes, fmtTs, protoLabel } from "../format.js";

defineProps({
  rows: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 20 },
  loading: { type: Boolean, default: false },
});
defineEmits(["page"]);
</script>

<style scoped>
.table-wrap { overflow: auto; }
table { width: 100%; border-collapse: collapse; font-size: 12px; }
th, td { text-align: left; padding: 7px 9px; border-bottom: 1px solid #1b2944; white-space: nowrap; }
th { color: var(--muted); font-weight: 500; position: sticky; top: 0; background: var(--panel); }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
.empty { text-align: center; color: var(--muted); padding: 22px 0; }
.proto {
  display: inline-block; padding: 1px 7px; border-radius: 10px;
  background: #0e3a52; color: #7dd3fc; font-size: 11px;
}
.proto.udp { background: #3b2f63; color: #c4b5fd; }
.proto.icmp { background: #33502f; color: #86efac; }
.pager { display: flex; justify-content: space-between; align-items: center; margin-top: 10px; }
</style>