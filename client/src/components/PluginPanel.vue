<!-- 界面插件面板：按服务端下发的声明式清单渲染 kpi / table / text。
     这里不执行任何插件代码——只有数据请求 + 白名单类型的渲染，因此没有代码注入面。 -->
<template>
  <section class="card pg-panel">
    <div class="pg-head">
      <span class="pg-icon">{{ spec.icon }}</span>
      <span class="pg-title">{{ spec.title }}</span>
      <span class="pg-status">{{ status }}</span>
      <span class="pg-grow"></span>
      <button class="btn" type="button" :disabled="loading" @click="refresh">刷新</button>
    </div>
    <div v-if="!available.ok" class="pg-banner">{{ available.hint }}，面板内容可能不完整</div>
    <div v-for="(w, i) in spec.widgets" :key="i" class="pg-block">
      <div v-if="w.title" class="pg-block-title">{{ w.title }}</div>
      <p v-if="w.type === 'text'" class="pg-text">{{ w.text }}</p>
      <div v-else-if="w.type === 'kpi'" class="pg-kpis">
        <div v-for="f in w.fields" :key="f.key" class="pg-kpi">
          <div class="pg-kpi-label">{{ f.label }}</div>
          <div class="pg-kpi-value">{{ fieldText(i, f) }}</div>
        </div>
      </div>
      <template v-else>
        <div v-if="!rows(i, w).length" class="pg-empty">{{ err(i) || "暂无数据（数据会随采集逐步累积）" }}</div>
        <table v-else class="pg-table">
          <thead>
            <tr>
              <th v-for="c in w.columns" :key="c.key">{{ c.label }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, ri) in rows(i, w)" :key="ri">
              <td v-for="c in w.columns" :key="c.key">{{ cellText(row, c) }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="err(i) && rows(i, w).length" class="pg-empty">{{ err(i) }}</div>
      </template>
    </div>
  </section>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { apiGet, errText } from "../api.js";
import { extractRows, formatUnit, pluginAvailability, widgetQuery } from "../pluginregistry.js";

const props = defineProps({
  spec: { type: Object, required: true },
  features: { type: Array, default: () => [] },
});

const payloads = reactive({});
const errors = reactive({});
const loading = ref(false);
const updatedAt = ref("");
let timer = null;

const available = computed(() => pluginAvailability(props.spec, props.features));
const status = computed(() => {
  if (loading.value) return "加载中…";
  return updatedAt.value ? "更新于 " + updatedAt.value : "手动刷新";
});

function reset() {
  for (const k of Object.keys(payloads)) delete payloads[k];
  for (const k of Object.keys(errors)) delete errors[k];
  updatedAt.value = "";
}

async function refresh() {
  const widgets = Array.isArray(props.spec.widgets) ? props.spec.widgets : [];
  const targets = widgets.map((w, i) => [i, w]).filter(([, w]) => w.type !== "text" && w.endpoint);
  if (!targets.length) return;
  loading.value = true;
  await Promise.all(
    targets.map(async ([i, w]) => {
      try {
        payloads[i] = await apiGet(widgetQuery(w));
        errors[i] = "";
      } catch (e) {
        payloads[i] = null;
        errors[i] = "读取失败：" + errText(e);
      }
    }),
  );
  loading.value = false;
  updatedAt.value = new Date().toLocaleTimeString();
}

function rows(i, w) {
  return extractRows(payloads[i], w.root);
}

function fieldText(i, f) {
  const p = payloads[i];
  return p && typeof p === "object" ? formatUnit(p[f.key], f.unit) : "-";
}

function cellText(row, c) {
  return formatUnit(row ? row[c.key] : null, c.unit);
}

function err(i) {
  return errors[i] || "";
}

function stopTimer() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

// refresh_s > 0 时按秒自动刷新（服务端已把取值夹在 0~600）
function startTimer() {
  stopTimer();
  const secs = Number(props.spec.refresh_s) || 0;
  if (secs > 0) timer = setInterval(refresh, secs * 1000);
}

watch(
  () => props.spec && props.spec.id,
  () => {
    reset();
    refresh();
    startTimer();
  },
);

onMounted(() => {
  refresh();
  startTimer();
});
onUnmounted(stopTimer);
</script>

<style scoped>
.pg-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pg-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.pg-icon {
  font-size: 18px;
}
.pg-title {
  font-weight: 600;
}
.pg-status {
  color: var(--muted);
  font-size: 12px;
}
.pg-grow {
  flex: 1;
}
.pg-banner {
  border: 1px solid var(--warn);
  color: var(--warn);
  border-radius: 8px;
  padding: 6px 10px;
  font-size: 12px;
}
.pg-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pg-block-title {
  color: var(--muted);
  font-size: 12px;
}
.pg-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--text);
}
.pg-kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 10px;
}
.pg-kpi {
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 8px 10px;
  background: rgb(var(--panel2-rgb) / 0.6);
}
.pg-kpi-label {
  color: var(--muted);
  font-size: 12px;
}
.pg-kpi-value {
  font-size: 18px;
  font-weight: 600;
}
.pg-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.pg-table th,
.pg-table td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--line);
}
.pg-table th {
  color: var(--muted);
  font-weight: 500;
}
.pg-empty {
  color: var(--muted);
  font-size: 12px;
}
</style>