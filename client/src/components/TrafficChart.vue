<template>
  <div class="card chart-card">
    <div class="chart-head">
      <span>历史流量曲线（最近 1 小时）</span>
      <span class="muted">{{ updatedText }}</span>
    </div>
    <div ref="el" class="chart"></div>
  </div>
</template>

<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import * as echarts from "echarts";

const props = defineProps({
  points: { type: Array, default: () => [] },
});
const el = ref(null);
const updatedText = ref("");
let chart = null;

function buildOption(points) {
  const times = points.map((p) => new Date(p.t * 1000));
  const bps = points.map((p) => Number(p.bps || 0));
  const pps = points.map((p) => Number(p.pps || 0));
  return {
    backgroundColor: "transparent",
    tooltip: {
      trigger: "axis",
      backgroundColor: "#0d1728",
      borderColor: "#243450",
      textStyle: { color: "#e5edf7", fontSize: 12 },
      valueFormatter: (v) => Number(v || 0).toLocaleString(),
    },
    legend: { data: ["带宽", "包速率"], textStyle: { color: "#8ea3bf" }, top: 0 },
    grid: { left: 58, right: 58, top: 34, bottom: 28 },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: "#243450" } },
      axisLabel: { color: "#8ea3bf", formatter: (v) => {
        const d = new Date(v);
        const p = (x) => String(x).padStart(2, "0");
        return p(d.getHours()) + ":" + p(d.getMinutes());
      } },
      splitLine: { show: false },
    },
    yAxis: [
      {
        type: "value",
        name: "带宽",
        nameTextStyle: { color: "#8ea3bf" },
        axisLabel: { color: "#8ea3bf", formatter: (v) => (v / 1e6).toFixed(0) + " Mb" },
        splitLine: { lineStyle: { color: "#1b2944" } },
      },
      {
        type: "value",
        name: "pps",
        nameTextStyle: { color: "#8ea3bf" },
        axisLabel: { color: "#8ea3bf" },
        splitLine: { show: false },
      },
    ],
    series: [
      {
        name: "带宽",
        type: "line",
        showSymbol: false,
        smooth: true,
        data: times.map((t, i) => [t, bps[i]]),
        lineStyle: { width: 2, color: "#22d3ee" },
        itemStyle: { color: "#22d3ee" },
        areaStyle: { color: "rgba(34,211,238,0.12)" },
      },
      {
        name: "包速率",
        type: "line",
        yAxisIndex: 1,
        showSymbol: false,
        smooth: true,
        data: times.map((t, i) => [t, pps[i]]),
        lineStyle: { width: 1.5, color: "#a78bfa" },
        itemStyle: { color: "#a78bfa" },
      },
    ],
  };
}

function render() {
  if (!chart) return;
  chart.setOption(buildOption(props.points || []), true);
  const last = (props.points || []).at(-1);
  updatedText.value = last ? "更新于 " + new Date(last.t * 1000).toLocaleTimeString() : "";
}

onMounted(() => {
  chart = echarts.init(el.value);
  render();
  window.addEventListener("resize", onResize);
});
onBeforeUnmount(() => {
  window.removeEventListener("resize", onResize);
  if (chart) {
    chart.dispose();
    chart = null;
  }
});
function onResize() {
  if (chart) chart.resize();
}
watch(() => props.points, render, { deep: false });
</script>

<style scoped>
.chart-card { display: flex; flex-direction: column; gap: 8px; }
.chart-head { display: flex; justify-content: space-between; font-weight: 600; }
.chart { height: 260px; width: 100%; }
</style>