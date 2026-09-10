<template>
  <div class="card chart-card">
    <div class="chart-head">
      <span>历史流量曲线（最近 1 小时）</span>
      <span class="muted">{{ updatedText }}</span>
    </div>
    <div class="chart-toolbar">
      <button
        v-for="m in modes"
        :key="m.value"
        class="mode-btn"
        :class="{ active: mode === m.value }"
        type="button"
        :title="m.label"
        @click="$emit('update-mode', m.value)"
      >
        <svg class="mode-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <template v-if="m.value === 'line'">
            <polyline points="3,17 8,9 13,13 21,5" />
          </template>
          <template v-else-if="m.value === 'area'">
            <path d="M3 17 L8 9 L13 13 L21 5 L21 20 L3 20 Z" fill="currentColor" opacity="0.28" stroke="none" />
            <polyline points="3,17 8,9 13,13 21,5" />
          </template>
          <template v-else>
            <rect x="4" y="13" width="4" height="7" rx="1" />
            <rect x="10" y="8" width="4" height="12" rx="1" />
            <rect x="16" y="4" width="4" height="16" rx="1" />
          </template>
        </svg>
        <span>{{ m.label }}</span>
      </button>
    </div>
    <div ref="el" class="chart"></div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import * as echarts from "echarts";

const props = defineProps({
  points: { type: Array, default: () => [] },
  theme: { type: String, default: "dark" },
  mode: { type: String, default: "line" },
});
defineEmits(["update-mode"]);

const el = ref(null);
const updatedText = ref("");
let chart = null;

const modes = [
  { value: "line", label: "折线图" },
  { value: "area", label: "面积图" },
  { value: "bar", label: "柱状图" },
];

const palette = computed(() =>
  props.theme === "light"
    ? {
        legend: "#5b6b81",
        axis: "#94a3b8",
        label: "#5b6b81",
        split: "#e2e8f0",
        bps: "#0891b2",
        bps2: "#0e7490",
        pps: "#7c3aed",
        pps2: "#6d28d9",
        tooltipBg: "#ffffff",
        tooltipBorder: "#d7e0ea",
        tooltipText: "#0f1b2d",
      }
    : {
        legend: "#8ea3bf",
        axis: "#243450",
        label: "#8ea3bf",
        split: "#1b2944",
        bps: "#22d3ee",
        bps2: "#0e7490",
        pps: "#a78bfa",
        pps2: "#7c3aed",
        tooltipBg: "#0d1728",
        tooltipBorder: "#243450",
        tooltipText: "#e5edf7",
      },
);

function buildOption(points, mode, p) {
  const times = points.map((pt) => new Date(pt.t * 1000));
  const bps = points.map((pt) => Number(pt.bps || 0));
  const pps = points.map((pt) => Number(pt.pps || 0));

  const lineSeries = (name, data, color, axis) => ({
    name,
    type: "line",
    yAxisIndex: axis,
    showSymbol: false,
    smooth: true,
    data,
    lineStyle: { width: 2, color },
    itemStyle: { color },
  });

  let series;
  if (mode === "bar") {
    // 柱状图：带宽与包速率都用柱形（双 Y 轴），明显区别于折线
    series = [
      {
        name: "带宽",
        type: "bar",
        barMaxWidth: 14,
        itemStyle: { color: p.bps, opacity: 0.85, borderRadius: [3, 3, 0, 0] },
        data: times.map((t, i) => [t, bps[i]]),
      },
      {
        name: "包速率",
        type: "bar",
        yAxisIndex: 1,
        barMaxWidth: 14,
        itemStyle: { color: p.pps, opacity: 0.7, borderRadius: [3, 3, 0, 0] },
        data: times.map((t, i) => [t, pps[i]]),
      },
    ];
  } else if (mode === "area") {
    const bpsSeries = lineSeries(
      "带宽",
      times.map((t, i) => [t, bps[i]]),
      p.bps,
      0,
    );
    bpsSeries.areaStyle = {
      color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: p.bps },
        { offset: 1, color: "rgba(34,211,238,0.02)" },
      ]),
      opacity: 0.45,
    };
    series = [bpsSeries, lineSeries("包速率", times.map((t, i) => [t, pps[i]]), p.pps, 1)];
  } else {
    series = [
      lineSeries("带宽", times.map((t, i) => [t, bps[i]]), p.bps, 0),
      lineSeries("包速率", times.map((t, i) => [t, pps[i]]), p.pps, 1),
    ];
  }

  return {
    backgroundColor: "transparent",
    tooltip: {
      trigger: "axis",
      backgroundColor: p.tooltipBg,
      borderColor: p.tooltipBorder,
      textStyle: { color: p.tooltipText, fontSize: 12 },
      valueFormatter: (v) => Number(v || 0).toLocaleString(),
    },
    legend: { data: ["带宽", "包速率"], textStyle: { color: p.legend }, top: 0 },
    grid: { left: 60, right: 60, top: 34, bottom: 28 },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: p.axis } },
      axisLabel: {
        color: p.label,
        formatter: (v) => {
          const d = new Date(v);
          const pad = (x) => String(x).padStart(2, "0");
          return pad(d.getHours()) + ":" + pad(d.getMinutes());
        },
      },
      splitLine: { show: false },
    },
    yAxis: [
      {
        type: "value",
        name: "带宽",
        nameTextStyle: { color: p.label },
        axisLabel: { color: p.label, formatter: (v) => (v / 1e6).toFixed(0) + " Mb" },
        splitLine: { lineStyle: { color: p.split } },
      },
      {
        type: "value",
        name: "pps",
        nameTextStyle: { color: p.label },
        axisLabel: { color: p.label },
        splitLine: { show: false },
      },
    ],
    series,
  };
}

function render() {
  if (!chart) return;
  chart.setOption(buildOption(props.points || [], props.mode, palette.value), true);
  const last = (props.points || []).at(-1);
  updatedText.value = last ? "更新于 " + new Date(last.t * 1000).toLocaleTimeString() : "";
}

function onResize() {
  if (chart) chart.resize();
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
watch(() => [props.points, props.theme, props.mode], render);
</script>

<style scoped>
.chart-card { display: flex; flex-direction: column; gap: 8px; }
.chart-head { display: flex; justify-content: space-between; font-weight: 600; }
.chart-toolbar { display: flex; gap: 6px; }
.mode-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--panel2);
  border: 1px solid var(--line);
  color: var(--muted);
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 12px;
  transition: all 0.2s;
}
.mode-btn:hover { border-color: var(--accent); color: var(--accent); }
.mode-btn.active { background: var(--accent); border-color: var(--accent); color: #06222b; font-weight: 600; }
.mode-ico { width: 16px; height: 16px; }
.chart { height: 270px; width: 100%; }
</style>