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
        <input v-model="form.name" placeholder="例如：CentOS" @keyup.enter="connect" />
      </div>
      <div class="field">
        <label>服务器地址</label>
        <input v-model="form.base" placeholder="192.168.1.100:8080" @keyup.enter="connect" />
      </div>
      <div class="field">
        <label>访问令牌（可选）</label>
        <div class="token-row">
          <input
            v-model="form.token"
            :type="showToken ? 'text' : 'password'"
            placeholder="Linux 端 api.token"
            @keyup.enter="connect"
          />
          <button class="btn eye" type="button" @click="showToken = !showToken">
            {{ showToken ? "隐藏" : "显示" }}
          </button>
        </div>
      </div>

      <div class="row">
        <button class="btn primary grow" type="button" :disabled="connecting" @click="connect">
          {{ connecting ? "连接中…" : connected ? "重新连接" : "连接" }}
        </button>
        <button class="btn" type="button" @click="saveProfile">保存</button>
        <button v-if="connected" class="btn danger" type="button" @click="disconnect">断开</button>
      </div>

      <div v-if="banner.show" class="banner" :class="'banner-' + banner.kind">
        {{ banner.text }}
      </div>

      <div class="profiles">
        <div class="muted section-title">已保存的连接</div>
        <div v-if="!profiles.length" class="muted empty">还没有保存的连接</div>
        <div v-for="(p, i) in profiles" :key="i" class="profile">
          <button class="profile-main" type="button" @click="useProfile(p)">
            <span class="dot" :class="{ on: p.base === form.base && connected }"></span>
            <span>{{ p.name }}</span>
          </button>
          <button class="profile-del" type="button" title="删除" @click="deleteProfile(p.name)">×</button>
        </div>
      </div>

      <div class="status">
        <span class="dot big" :class="statusView_.cls"></span>
        <span>{{ statusView_.text }}</span>
        <span class="muted kernel" :class="ipcClass">内核：{{ kernelText }}</span>
      </div>

      <div class="logbox">
        <div class="muted section-title">连接日志</div>
        <div v-if="!uiLog.length" class="muted empty">暂无记录，点击"连接"后这里会显示每一步</div>
        <div v-for="(line, i) in uiLog" :key="i" class="log-line">{{ line }}</div>
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
          <button class="btn" type="button" :disabled="sessLoading" @click="loadSessions(1)">刷新</button>
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
          在左侧填写 Linux 上 netmon 的 IP 与端口（默认 8080），点击"连接"即可实时查看带宽、会话与健康状态。
        </p>
        <p class="muted small">提示：地址可填 192.168.1.100:8080，也可带 http:// 前缀；若 Linux 端配置了 api.token 请一并填写。</p>
      </div>
    </main>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import KpiCards from "./components/KpiCards.vue";
import TrafficChart from "./components/TrafficChart.vue";
import SessionTable from "./components/SessionTable.vue";
import { apiGet, clearConnection, errText, ping, setConnection } from "./api.js";
import { fmtDuration, fmtTs, normalizeBaseUrl } from "./format.js";
import { connectMessage, statusView } from "./status.js";

const STORE_KEY = "netmon.profiles.v1";
const pageSize = 20;

const form = reactive({ name: "", base: "", token: "" });
const profiles = ref(loadProfiles());
const connecting = ref(false);
const connected = ref(false);
const now = reactive({});
const history = ref([]);
const sessRows = ref([]);
const sessTotal = ref(0);
const sessPage = ref(1);
const sessLoading = ref(false);
const sessFilter = reactive({ ip: "", proto: "" });
const showToken = ref(false);
const uiLog = ref([]);
const banner = reactive({ show: false, kind: "busy", text: "" });

const toast = reactive({ show: false, type: "ok", text: "" });
let toastTimer = null;
let nowTimer = null;
let historyTimer = null;
let sessionTimer = null;
let failCount = 0;

const statusView_ = computed(() =>
  statusView(connected.value, form.name || form.base, banner.kind === "err"),
);
const ipcOk = ref(null);
const kernelText = computed(() =>
  ipcOk.value === true ? "正常" : ipcOk.value === false ? "异常" : "检测中",
);
const ipcClass = computed(() =>
  ipcOk.value === true ? "k-ok" : ipcOk.value === false ? "k-bad" : "",
);

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

function pushLog(msg) {
  const t = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  uiLog.value.push(`${t} ${msg}`);
  if (uiLog.value.length > 80) uiLog.value.shift();
}

function setBanner(kind, text) {
  banner.kind = kind;
  banner.text = text;
  banner.show = text !== "";
}

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
    setBanner("err", "请先填写连接名称");
    return;
  }
  const norm = normalizeBaseUrl(form.base);
  if (!norm.ok) {
    setBanner("err", norm.error);
    return;
  }
  const idx = profiles.value.findIndex((p) => p.name === form.name.trim());
  const item = { name: form.name.trim(), base: norm.url, token: form.token || "" };
  if (idx >= 0) profiles.value[idx] = item;
  else profiles.value.push(item);
  persistProfiles();
  setBanner("ok", `已保存连接：${item.name}`);
}
function useProfile(p) {
  form.name = p.name;
  form.base = p.base;
  form.token = p.token || "";
  setBanner("ok", `已载入连接：${p.name}`);
}
function deleteProfile(name) {
  profiles.value = profiles.value.filter((p) => p.name !== name);
  persistProfiles();
}

function withTimeout(promise, ms, label) {
  return Promise.race([
    promise,
    new Promise((_, reject) =>
      setTimeout(
        () => reject(new Error(`${label}超时（${Math.round(ms / 1000)} 秒无响应）`)),
        ms,
      ),
    ),
  ]);
}

async function connect() {
  pushLog("点击连接");
  const norm = normalizeBaseUrl(form.base);
  if (!norm.ok) {
    setBanner("err", norm.error);
    pushLog("校验失败：" + norm.error);
    return;
  }
  const label = form.name || norm.url;
  connecting.value = true;
  setBanner("busy", "正在连接 " + label + " …");
  pushLog("地址校验通过：" + norm.url);
  notify({ type: "ok", text: "正在连接 " + label + " …" });
  try {
    pushLog("调用内核：set_connection（保存连接配置）");
    await withTimeout(setConnection(norm.url, form.token), 10000, "初始化连接");
    pushLog("内核：set_connection 完成");
    pushLog("请求实时数据：/api/v1/traffic/now");
    await withTimeout(apiGet("/api/v1/traffic/now"), 15000, "请求实时数据");
    pushLog("实时数据请求成功");
    connected.value = true;
    failCount = 0;
    setBanner("ok", "已连接：" + label);
    notify(connectMessage("ok", label));
    startTimers();
    await Promise.all([refreshNow(), refreshHistory(), loadSessions(1)]);
  } catch (e) {
    connected.value = false;
    const reason = errText(e);
    setBanner("err", "连接失败：" + reason);
    pushLog("失败：" + reason);
    notify(connectMessage("err", label, reason));
    stopTimers();
  } finally {
    connecting.value = false;
  }
}

async function disconnect() {
  stopTimers();
  connected.value = false;
  failCount = 0;
  setBanner("ok", "已断开连接");
  pushLog("已断开连接");
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
      const reason = errText(e);
      setBanner("err", "连接中断：" + reason);
      pushLog("连接中断：" + reason);
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

onMounted(async () => {
  pushLog("客户端启动，进行 IPC 自检…");
  try {
    await withTimeout(ping(), 3000, "IPC 自检");
    ipcOk.value = true;
    pushLog("IPC 自检通过（内核通道正常）");
  } catch (e) {
    ipcOk.value = false;
    pushLog("IPC 自检失败：" + errText(e));
  }
});

onBeforeUnmount(() => {
  stopTimers();
  if (toastTimer) clearTimeout(toastTimer);
});
</script>

<style scoped>
.layout { display: flex; height: 100%; gap: 12px; padding: 12px; }
.sidebar { width: 320px; flex-shrink: 0; display: flex; flex-direction: column; gap: 10px; overflow: auto; }
.brand { display: flex; gap: 10px; align-items: center; margin-bottom: 6px; }
.logo {
  width: 38px; height: 38px; border-radius: 10px; background: linear-gradient(135deg, #0e7490, #22d3ee);
  display: flex; align-items: center; justify-content: center; font-weight: 800; font-size: 20px; color: #06222b;
}
.brand-title { font-weight: 700; font-size: 15px; }
.field { display: flex; flex-direction: column; gap: 4px; }
.field label { color: var(--muted); font-size: 12px; }
.token-row { display: flex; gap: 6px; }
.token-row input { flex: 1; }
.eye { flex-shrink: 0; }
.row { display: flex; gap: 6px; }
.grow { flex: 1; }
.banner {
  padding: 8px 10px;
  border-radius: 7px;
  font-size: 12px;
  word-break: break-all;
}
.banner-busy { background: #12314a; color: #bae6fd; border: 1px solid #0e7490; }
.banner-ok { background: #064e3b; color: #d1fae5; border: 1px solid #10b981; }
.banner-err { background: #450a0a; color: #fee2e2; border: 1px solid #ef4444; }
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
.profile-del { background: none; border: none; color: var(--muted); font-size: 16px; padding: 2px 6px; }
.profile-del:hover { color: var(--danger); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: #475569; flex-shrink: 0; }
.dot.on { background: var(--ok); box-shadow: 0 0 6px var(--ok); }
.dot.big { width: 9px; height: 9px; }
.dot.ok { background: var(--ok); }
.dot.bad { background: var(--danger); }
.status { display: flex; align-items: center; gap: 7px; font-size: 12px; color: var(--muted); }
.kernel { font-size: 11px; margin-left: auto; }
.kernel.k-ok { color: var(--ok); }
.kernel.k-bad { color: var(--danger); }
.logbox { margin-top: 2px; }
.log-line {
  font-family: Consolas, monospace;
  font-size: 11px;
  color: var(--muted);
  padding: 1px 0;
  border-bottom: 1px dashed #1b2944;
  word-break: break-all;
}
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 12px; overflow: auto; }
.center { align-items: center; justify-content: center; }
.welcome { max-width: 540px; text-align: center; padding: 30px; }
.welcome h2 { margin: 0 0 10px; }
.small { font-size: 12px; }
.info-line { display: flex; gap: 16px; font-size: 12px; flex-wrap: wrap; }
.sess-head { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; }
.sess-title { font-weight: 700; margin-right: auto; }
.filter-ip { width: 170px; }
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
</style>