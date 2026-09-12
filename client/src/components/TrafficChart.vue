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
          <template v-else>
            <path d="M3 17 L8 9 L13 13 L21 5 L21 20 L3 20 Z" fill="currentColor" opacity="0.28" stroke="none" />
            <polyline points="3,17 8,9 13,13 21,5" />
          </template>
        </svg>
        <span>{{ m.label }}</span>
      </button>
      <span class="tool-sep"></span>
      <button class="mode-btn" type="button" title="放大时间轴（也可用鼠标滚轮）" @click="applyZoom('in')">放大 ＋</button>
      <button class="mode-btn" type="button" title="缩小时间轴" @click="applyZoom('out')">缩小 －</button>
      <button class="mode-btn" type="button" title="恢复完整时间范围" @click="applyZoom('reset')">重置</button>
      <span class="muted zoom-info">{{ zoomText }}</span>
    </div>
    <div class="chart-toolbar axis-row">
      <span class="muted axis-label">左轴上限</span>
      <input v-model="axisForm.mbps" class="axis-input" inputmode="decimal" placeholder="自动" @change="applyAxis" />
      <span class="muted axis-unit">Mb/s</span>
      <span class="muted axis-label">右轴上限</span>
      <input v-model="axisForm.pps" class="axis-input" inputmode="decimal" placeholder="自动" @change="applyAxis" />
      <span class="muted axis-unit">pps</span>
      <button class="mode-btn" type="button" title="清空手动值，恢复自动坐标轴" @click="resetAxis">自动</button>
      <span class="muted zoom-info">{{ axisText }}</span>
    </div>
    <div ref="el" class="chart"></div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import * as echarts from "echarts";
import { clampRange, zoomLabel, zoomRange } from "../chartzoom.js";
import { AXIS_STORE_KEY, axisHint, axisMax, autoAxisMax, normalizeAxisConfig, parseAxisInput } from "../axis.js";

const props = defineProps({
  points: { type: Array, default: () => [] },
  theme: { type: String, default: "dark" },
  mode: { type: String, default: "line" },
});
defineEmits(["update-mode"]);

const el = ref(null);
const updatedText = ref("");
let chart = null;

// 缩放区间（百分比，与 ECharts dataZoom 一致）：默认展示完整时间范围
const zoom = ref({ start: 0, end: 100 });
const zoomText = computed(() => zoomLabel(zoom.value));

function applyZoom(direction) {
  zoom.value = zoomRange(zoom.value, direction);
}

// 用户用滚轮或拖动缩放条后，把 ECharts 的区间同步回组件状态（相同则忽略，避免循环）
function onDataZoom() {
  if (!chart) return;
  const opt = chart.getOption();
  const dz = (opt.dataZoom && opt.dataZoom[0]) || {};
  const next = clampRange(dz.start, dz.end);
  if (Math.abs(next.start - zoom.value.start) < 0.01 && Math.abs(next.end - zoom.value.end) < 0.01) return;
  zoom.value = next;
}

// 曲线样式：只保留折线与面积（柱状图已按需求移除）
const modes = [
  { value: "line", label: "折线图" },
  { value: "area", label: "面积图" },
];

// 左右坐标轴手动上限：留空 = 自动（按量级取整，不会一直跳动）
function loadAxisConfig() {
  try {
    return normalizeAxisConfig(JSON.parse(localStorage.getItem(AXIS_STORE_KEY) || "{}"));
  } catch {
    return normalizeAxisConfig({});
  }
}
const axisConfig = ref(loadAxisConfig());
const axisForm = reactive({ mbps: axisConfig.value.mbps ?? "", pps: axisConfig.value.pps ?? "" });
const axisText = computed(() => axisHint(axisConfig.value));

function applyAxis() {
  axisConfig.value = normalizeAxisConfig({
    mbps: parseAxisInput(axisForm.mbps, 1e6),
    pps: parseAxisInput(axisForm.pps, 1e9),
  });
  axisForm.mbps = axisConfig.value.mbps ?? "";
  axisForm.pps = axisConfig.value.pps ?? "";
  try {
    localStorage.setItem(AXIS_STORE_KEY, JSON.stringify(axisConfig.value));
  } catch {
    // 忽略存储失败
  }
}

function resetAxis() {
  axisForm.mbps = "";
  axisForm.pps = "";
  applyAxis();
}

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

function buildOption(points, mode, p, z, a) {
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
  if (mode === "area") {
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

  // 坐标轴上限：手动值优先；自动时按量级取整，数字只在跨档位时变化
  const leftMax = a && a.left !== undefined ? a.left : autoAxisMax(Math.max(0, ...bps));
  const rightMax = a && a.right !== undefined ? a.right : autoAxisMax(Math.max(0, ...pps));

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
    grid: { left: 60, right: 60, top: 34, bottom: 48 },
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
        min: 0,
        max: leftMax,
        nameTextStyle: { color: p.label },
        axisLabel: { color: p.label, formatter: (v) => (v / 1e6).toFixed(0) + " Mb" },
        splitLine: { lineStyle: { color: p.split } },
      },
      {
        type: "value",
        name: "pps",
        min: 0,
        max: rightMax,
        nameTextStyle: { color: p.label },
        axisLabel: { color: p.label },
        splitLine: { show: false },
      },
    ],
    dataZoom: [
      { type: "inside", start: z.start, end: z.end, zoomOnMouseWheel: true, moveOnMouseMove: true },
      {
        type: "slider",
        start: z.start,
        end: z.end,
        height: 16,
        bottom: 6,
        borderColor: p.split,
        fillerColor: "rgba(34,211,238,0.14)",
        handleStyle: { color: p.bps },
        textStyle: { color: p.label, fontSize: 10 },
      },
    ],
    series,
  };
}

function render() {
  if (!chart) return;
  chart.setOption(buildOption(props.points || [], props.mode, palette.value, zoom.value, axisMax(axisConfig.value)), true);
  const last = (props.points || []).at(-1);
  updatedText.value = last ? "更新于 " + new Date(last.t * 1000).toLocaleTimeString() : "";
}

function onResize() {
  if (chart) chart.resize();
}

onMounted(() => {
  chart = echarts.init(el.value);
  chart.on("datazoom", onDataZoom);
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
watch(() => [props.points, props.theme, props.mode, zoom.value, axisConfig.value], render);
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
.tool-sep { width: 1px; height: 18px; background: var(--line); margin: 0 2px; }
.axis-row { align-items: center; flex-wrap: wrap; gap: 6px; }
.axis-label { font-size: 12px; }
.axis-unit { font-size: 11px; }
.axis-input {
  width: 84px;
  background: var(--panel2);
  border: 1px solid var(--line);
  color: var(--text);
  border-radius: 6px;
  padding: 3px 7px;
  font-size: 12px;
  outline: none;
}
.axis-input:focus { border-color: var(--accent); }
.chart { height: 300px; width: 100%; }
.zoom-info { font-size: 11px; align-self: center; }
</style>