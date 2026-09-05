<template>
  <div class="kpi-grid">
    <div class="card kpi">
      <div class="kpi-label">实时带宽</div>
      <div class="kpi-value">{{ fmtRate(now.bps) }}</div>
      <div class="kpi-sub">累计收包 {{ fmtNum(now.recv_packets) }}</div>
    </div>
    <div class="card kpi">
      <div class="kpi-label">包速率</div>
      <div class="kpi-value">{{ fmtPps(now.pps) }}</div>
      <div class="kpi-sub">累计丢包 {{ fmtNum(now.drop_packets) }}</div>
    </div>
    <div class="card kpi">
      <div class="kpi-label">并发连接</div>
      <div class="kpi-value">{{ fmtNum(now.conns) }}</div>
      <div class="kpi-sub">新增 {{ fmtNum(now.new_conns) }}</div>
    </div>
    <div class="card kpi" :class="{ warn: dropWarn }">
      <div class="kpi-label">丢包率</div>
      <div class="kpi-value">{{ pct(now.drop_rate) }}</div>
      <div class="kpi-sub">{{ dropWarn ? "发生抓包丢包，指标可能失真" : "状态正常" }}</div>
    </div>
  </div>
</template>

<script setup>
import { computed } from "vue";
import { fmtRate, fmtPps, fmtNum } from "../format.js";

const props = defineProps({
  now: { type: Object, default: () => ({}) },
});
const now = computed(() => props.now || {});
const dropWarn = computed(() => Number(now.value.drop_rate || 0) > 0);
function pct(v) {
  const n = Number(v || 0);
  return (n * 100).toFixed(n > 0 && n < 0.01 ? 3 : 2) + "%";
}
</script>

<style scoped>
.kpi-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 10px;
}
.kpi { text-align: left; }
.kpi-label { color: var(--muted); font-size: 12px; }
.kpi-value { font-size: 22px; font-weight: 600; margin: 6px 0 4px; }
.kpi-sub { color: var(--muted); font-size: 11px; }
.kpi.warn { border-color: var(--warn); }
.kpi.warn .kpi-value { color: var(--warn); }
</style>