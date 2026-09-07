<template>
  <div class="layout">
    <transition name="toast">
      <div v-if="toast.show" class="toast" :class="'toast-' + toast.type" @click="toast.show = false">
        {{ toast.text }}
      </div>
    </transition>
    <aside class="sidebar card">
      <div class="brand">
        <div class="logo">N</div>
        <div>
          <div class="brand-title">网络流量监控</div>
          <div class="muted">Windows 客户端 · V0.3</div>
        </div>
      </div>

      <div class="field">
        <label>连接名称</label>
        <input v-model="form.name" placeholder="例如：我的 CentOS" />
      </div>
      <div class="field">
        <label>服务器地址</label>
        <input v-model="form.base" placeholder="192.168.1.100:8080" @keyup.enter="connect" />
      </div>
      <div class="field">
        <label>访问令牌（可选）</label>
        <input v-model="form.token" type="password" placeholder="Linux 端 api.token" @keyup.enter="connect" />
      </div>
      <div class="row">
        <button class="btn primary grow" :disabled="connecting" @click="connect">
          {{ connecting ? "连接中…" : connected ? "重新连接" : "连接" }}
        </button>
        <button class="btn" @click="saveProfile">保存</button>
        <button v-if="connected" class="btn danger" @click="disconnect">断开</button>
      </div>
      <div v-if="connError" class="error">{{ connError }}</div>

      <div class="profiles">
        <div class="muted section-title">已保存的连接</div>
        <div v-if="!profiles.length" class="muted empty">还没有保存的连接</div>
        <div v-for="(p, i) in profiles" :key="i" class="profile">
          <button class="profile-main" @click="useProfile(p)">
            <span class="dot" :class="{ on: p.base === form.base && connected }"></span>
            <span>{{ p.name }}</span>
          </button>
          <button class="profile-del" title="删除" @click="deleteProfile(p.name)">×</button>
        </div>
      </div>

      <div class="status">
        <span class="dot big" :class="statusView_.cls"></span>
        <span>{{ statusView_.text }}</span>
      </div>
    </aside>

    <main v-if="connected" class="main">
      <div class="info-line muted">
        <span>机器：{{ now.machine_id || "-" }}</span>
        <span>数据源：{{ now.source || "-" }}</span>
        <span>运行时长：{{ fmtDuration(now.uptime_s) }}</span>
        <span>活跃流：{{ now.flows_active ?? "-" }}</span>
        <span class="grow"></span>
        <span>最后更新：{{ fmtTs(now.ts) }}</span>
      </div>
      <KpiCards :now="now" />
      <TrafficChart :points="history" />

      <div class="card">
        <div class="sess-head">
          <span class="sess-title">会话明细</span>
          <input v-model="sessFilter.ip" class="filter-ip" placeholder="按 IP 过滤，如 10.0" />
          <select v-model="sessFilter.proto">
            <option value="">全部协议</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="icmp">ICMP</option>
          </select>
          <button class="btn" :disabled="sessLoading" @click="loadSessions(1)">刷新</button>
        </div>
        <SessionTable
          :rows="sessRows"
          :total="sessTotal"
          :page="sessPage"
          :page-size="pageSize"
          :loading="sessLoading"
          @page="loadSessions"
        />
      </div>
    </main>

    <main v-else class="main center">
      <div class="welcome card">
        <h2>连接你的 Linux 监控端</h2>
        <p class="muted">
          在左侧填写 Linux 上 netmon 的 IP 与端口（默认 8080），即可在 Windows 上实时查看带宽、会话与健康状态。
          若 Linux 端配置了 api.token，请一并填写。
        </p>
        <p class="muted small">提示：先在 Linux 端运行 netmon（synthetic / replay / live 均可），并放行防火墙端口。</p>
      </div>
    </main>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import KpiCards from "./components/KpiCards.vue";
import TrafficChart from "./components/TrafficChart.vue";
import SessionTable from "./components/SessionTable.vue";
import { apiGet, clearConnection, errText, setConnection } from "./api.js";
import { fmtDuration, fmtTs, normalizeBaseUrl } from "./format.js";
import { connectMessage, statusView } from "./status.js";

const STORE_KEY = "netmon.profiles.v1";
const pageSize = 20;

const form = reactive({ name: "", base: "", token: "" });
const profiles = ref(loadProfiles());
const connecting = ref(false);
const connected = ref(false);
const connError = ref("");
const now = reactive({});
const history = ref([]);
const sessRows = ref([]);
const sessTotal = ref(0);
const sessPage = ref(1);
const sessLoading = ref(false);
const sessFilter = reactive({ ip: "", proto: "" });

let nowTimer = null;
let historyTimer = null;
let sessionTimer = null;
let failCount = 0;
const toast = reactive({ show: false, type: "ok", text: "" });
let toastTimer = null;
function notify(msg) {
  if (!msg) return;
  toast.type = msg.type || "err";
  toast.text = msg.text || "";
  toast.show = true;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toast.show = false;
  }, 4000);
}

const statusView_ = computed(() =>
  statusView(connected.value, form.name || form.base, connError.value !== ""),
);

function loadProfiles() {
  try {
    const raw = localStorage.getItem(STORE_KEY);
    const arr = raw ? JSON.parse(raw) : [];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}
function persistProfiles() {
  localStorage.setItem(STORE_KEY, JSON.stringify(profiles.value));
}
function saveProfile() {
  if (!form.name.trim()) {
    connError = "请先填写连接名称";
    return;
  }
  const norm = normalizeBaseUrl(form.base);
  if (!norm.ok) {
    connError = norm.error;
    return;
  }
  const idx = profiles.value.findIndex((p) => p.name === form.name.trim());
  const item = { name: form.name.trim(), base: norm.url, token: form.token || "" };
  if (idx >= 0) profiles.value[idx] = item;
  else profiles.value.push(item);
  persistProfiles();
  connError = "";
}
function useProfile(p) {
  form.name = p.name;
  form.base = p.base;
  form.token = p.token || "";
  connError = "";
}
function deleteProfile(name) {
  profiles.value = profiles.value.filter((p) => p.name !== name);
  persistProfiles();
}

async function connect() {
  const norm = normalizeBaseUrl(form.base);
  if (!norm.ok) {
    connError = norm.error;
    notify(connectMessage("warn", form.name, norm.error));
    return;
  }
  connecting.value = true;
  connError = "";
  notify({ type: "ok", text: "正在连接 " + (form.name || norm.url) + " …" });
  try {
    await setConnection(norm.url, form.token);
    await apiGet("/api/v1/traffic/now");
    connected.value = true;
    failCount = 0;
    notify(connectMessage("ok", form.name || norm.url));
    startTimers();
    await Promise.all([refreshNow(), refreshHistory(), loadSessions(1)]);
  } catch (e) {
    connected.value = false;
    const reason = errText(e);
    connError = "连接失败：" + reason;
    notify(connectMessage("err", form.name || norm.url, reason));
    stopTimers();
  } finally {
    connecting.value = false;
  }
}

async function disconnect() {
  stopTimers();
  connected.value = false;
  connError = "";
  failCount = 0;
  notify({ type: "ok", text: "已断开连接" });
  try {
    await clearConnection();
  } catch {
    // 忽略断开时的清理错误
  }
}

function startTimers() {
  stopTimers();
  nowTimer = setInterval(refreshNow, 1000);
  historyTimer = setInterval(refreshHistory, 5000);
  sessionTimer = setInterval(() => loadSessions(sessPage.value), 8000);
}
function stopTimers() {
  if (nowTimer) clearInterval(nowTimer);
  if (historyTimer) clearInterval(historyTimer);
  if (sessionTimer) clearInterval(sessionTimer);
  nowTimer = historyTimer = sessionTimer = null;
}

async function refreshNow() {
  try {
    const data = await apiGet("/api/v1/traffic/now");
    Object.keys(now).forEach((k) => delete now[k]);
    Object.assign(now, data);
    failCount = 0;
  } catch (e) {
    failCount += 1;
    if (failCount >= 4) {
      connError = "连接中断：" + errText(e);
      connected.value = false;
      stopTimers();
    }
  }
}
async function refreshHistory() {
  try {
    const data = await apiGet("/api/v1/traffic/history?since=3600&step=60");
    history.value = (data && data.points) || [];
  } catch {
    // 轮询失败由 refreshNow 统一处理
  }
}
async function loadSessions(page) {
  if (!connected.value || sessLoading.value) return;
  sessLoading.value = true;
  sessPage.value = Math.max(1, page || 1);
  const params = new URLSearchParams();
  params.set("page", String(sessPage.value));
  params.set("page_size", String(pageSize));
  if (sessFilter.ip.trim()) params.set("ip", sessFilter.ip.trim());
  if (sessFilter.proto) params.set("proto", sessFilter.proto);
  try {
    const data = await apiGet("/api/v1/sessions?" + params.toString());
    sessRows.value = (data && data.items) || [];
    sessTotal.value = (data && data.total) || 0;
  } catch {
    // 列表失败不打断主流程
  } finally {
    sessLoading.value = false;
  }
}

let filterTimer = null;
watch(
  () => [sessFilter.ip, sessFilter.proto],
  () => {
    if (filterTimer) clearTimeout(filterTimer);
    filterTimer = setTimeout(() => loadSessions(1), 400);
  },
);

onBeforeUnmount(() => {
  stopTimers();
  if (toastTimer) clearTimeout(toastTimer);
});
</script>

<style scoped>
.layout { display: flex; height: 100%; gap: 12px; padding: 12px; }
.sidebar { width: 300px; flex-shrink: 0; display: flex; flex-direction: column; gap: 10px; overflow: auto; }
.brand { display: flex; gap: 10px; align-items: center; margin-bottom: 6px; }
.logo {
  width: 38px; height: 38px; border-radius: 10px; background: linear-gradient(135deg, #0e7490, #22d3ee);
  display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 20px; color: #06222b;
}
.brand-title { font-weight: 700; font-size: 15px; }
.field { display: flex; flex-direction: column; gap: 4px; }
.field label { color: var(--muted); font-size: 12px; }
.row { display: flex; gap: 6px; }
.grow { flex: 1; }
.error { color: var(--danger); font-size: 12px; word-break: break-all; }
.profiles { margin-top: 4px; }
.section-title { font-size: 12px; margin-bottom: 6px; }
.empty { font-size: 12px; }
.profile { display: flex; align-items: center; gap: 4px; margin-bottom: 4px; }
.profile-main {
  flex: 1; text-align: left; display: flex; align-items: center; gap: 7px;
  background: var(--panel2); border: 1px solid var(--line); color: var(--text);
  border-radius: 6px; padding: 6px 8px; font-size: 13px; overflow: hidden;
}
.profile-main:hover { border-color: var(--accent); }
.profile-del {
  background: none; border: none; color: var(--muted); font-size: 16px; padding: 2px 6px;
}
.profile-del:hover { color: var(--danger); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: #475569; flex-shrink: 0; }
.dot.on { background: var(--ok); box-shadow: 0 0 6px var(--ok); }
.dot.big { width: 9px; height: 9px; }
.dot.ok { background: var(--ok); }
.dot.bad { background: var(--danger); }
.status { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--muted); margin-top: auto; padding-top: 8px; }
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 12px; overflow: auto; }
.center { align-items: center; justify-content: center; }
.welcome { max-width: 520px; text-align: center; padding: 30px; }
.welcome h2 { margin: 0 0 10px; }
.toast {
  position: fixed;
  top: 18px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 99;
  padding: 9px 18px;
  border-radius: 8px;
  font-size: 13px;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.35);
  cursor: pointer;
  max-width: 70vw;
}
.toast-ok { background: #065f46; color: #d1fae5; border: 1px solid #10b981; }
.toast-err { background: #7f1d1d; color: #fee2e2; border: 1px solid #ef4444; }
.toast-enter-active, .toast-leave-active { transition: opacity 0.25s, transform 0.25s; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateX(-50%) translateY(-6px); }
.small { font-size: 12px; }
.info-line { display: flex; gap: 16px; font-size: 12px; flex-wrap: wrap; }
.sess-head { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; }
.sess-title { font-weight: 700; margin-right: auto; }
.filter-ip { width: 170px; }
</style>