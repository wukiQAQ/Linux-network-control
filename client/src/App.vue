<template>
  <div class="layout">
    <transition name="toast">
      <div v-if="toast.show" class="toast" :class="'toast-' + toast.type" @click="toast.show = false">
        {{ toast.text }}
      </div>
    </transition>

    <!-- 左侧图标导航栏：悬停放大并显示文字 -->
    <nav class="rail">
      <button
        v-for="item in navItems"
        :key="item.key"
        class="rail-btn"
        :class="{ active: nav === item.key }"
        type="button"
        :title="item.label"
        @click="nav = item.key"
      >
        <span class="rail-icon">{{ item.icon }}</span>
        <span class="rail-txt">{{ item.label }}</span>
      </button>
      <div class="rail-grow"></div>
      <button class="rail-btn" type="button" title="设置" @click="settingsOpen = !settingsOpen">
        <span class="rail-icon">⚙️</span>
        <span class="rail-txt">设置</span>
      </button>
    </nav>

    <!-- 账号管理 / 连接日志 面板 -->
    <aside v-if="nav === 'accounts'" class="side card">
      <div class="panel-title">账号管理</div>
      <div class="muted hint">点击"切换"即可切换连接，图表会自动刷新</div>

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
      <div v-if="banner.show" class="banner" :class="'banner-' + banner.kind">{{ banner.text }}</div>

      <div class="muted section-title">已保存的连接（切换自动刷新）</div>
      <div v-if="!profiles.length" class="muted empty">还没有保存的连接</div>
      <div v-for="(p, i) in profiles" :key="i" class="profile">
        <button
          class="profile-main"
          type="button"
          :class="{ active: connected && p.base === form.base }"
          @click="switchProfile(p)"
        >
          <span class="dot" :class="{ on: connected && p.base === form.base }"></span>
          <span class="profile-name">{{ p.name }}</span>
          <span class="muted profile-addr">{{ p.base }}</span>
        </button>
        <button class="btn small" type="button" @click="useProfile(p)">编辑</button>
        <button class="profile-del" type="button" title="删除" @click="deleteProfile(p.name)">×</button>
      </div>
    </aside>

    <aside v-else-if="nav === 'logs'" class="side card">
      <div class="panel-title">连接日志与状态</div>
      <div class="status-line">
        <span class="dot big" :class="statusView_.cls"></span>
        <span>{{ statusView_.text }}</span>
      </div>
      <div class="status-line">
        <span class="muted">内核通道：</span>
        <span :class="ipcOk === true ? 'k-ok' : ipcOk === false ? 'k-bad' : ''">
          {{ kernelText }}
        </span>
      </div>
      <div class="logbox">
        <div v-if="!uiLog.length" class="muted empty">暂无记录</div>
        <div v-for="(line, i) in uiLog" :key="i" class="log-line">{{ line }}</div>
      </div>
    </aside>

    <!-- 主内容 -->
    <main class="main">
      <template v-if="connected">
        <div class="info-line muted">
          <span>机器：{{ now.machine_id || "-" }}</span>
          <span>数据源：{{ now.source || "-" }}</span>
          <span>运行时长：{{ fmtDuration(now.uptime_s) }}</span>
          <span>活跃流：{{ now.flows_active ?? "-" }}</span>
          <span class="grow"></span>
          <span>最后更新：{{ fmtTs(now.ts) }}</span>
        </div>
        <KpiCards :now="now" />
        <TrafficChart :points="history" :theme="theme" :mode="chartMode" @update-mode="setChartMode" />
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
      </template>
      <div v-else class="welcome card">
        <h2>欢迎使用 MeTD</h2>
        <p class="muted">
          点击左侧「账号管理」填写 Linux 监控端地址（如 192.168.161.128:8080）并连接；
          或用齿轮进入设置调整主题、背景与托盘选项。
        </p>
      </div>
    </main>

    <!-- 设置弹窗 -->
    <div v-if="settingsOpen" class="overlay" @click.self="settingsOpen = false">
      <div class="dialog card">
        <div class="dialog-head">
          <span>设置</span>
          <button class="profile-del" type="button" @click="settingsOpen = false">×</button>
        </div>

        <div class="muted section-title">外观</div>
        <div class="seg">
          <button class="seg-btn" :class="{ active: theme === 'dark' }" type="button" @click="setTheme('dark')">🌙 夜间模式</button>
          <button class="seg-btn" :class="{ active: theme === 'light' }" type="button" @click="setTheme('light')">☀ 明亮模式</button>
        </div>
        <div class="field">
          <label>面板背景透明度：{{ Math.round(settings.opacity * 100) }}%（55% ~ 100%）</label>
          <input v-model.number="settings.opacity" type="range" min="0.55" max="1" step="0.05" @input="applyView" />
        </div>
        <div class="field">
          <label>自定义背景图片</label>
          <div class="row">
            <button class="btn" type="button" @click="pickBackground">选择图片</button>
            <button class="btn" type="button" @click="clearBackground">恢复默认</button>
          </div>
          <input ref="bgInput" type="file" accept="image/*" class="hidden" @change="onBgFile" />
        </div>

        <div class="muted section-title">启动与托盘</div>
        <div class="switch-row">
          <span>开机自启动</span>
          <button class="switch" :class="{ on: settings.autostart }" type="button" @click="toggleAutostart"></button>
        </div>
        <div class="switch-row">
          <span>启动时最小化到托盘</span>
          <button class="switch" :class="{ on: settings.startMinimized }" type="button" @click="settings.startMinimized = !settings.startMinimized; persistSettings()"></button>
        </div>
        <div class="switch-row">
          <span>关闭窗口时最小化到托盘</span>
          <button class="switch" :class="{ on: settings.closeToTray }" type="button" @click="settings.closeToTray = !settings.closeToTray; persistSettings()"></button>
        </div>
        <p class="muted small">托盘常驻：右键托盘图标可"显示 MeTD / 退出"；左键单击切换显示。</p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { getCurrentWindow } from "@tauri-apps/api/window";
import KpiCards from "./components/KpiCards.vue";
import TrafficChart from "./components/TrafficChart.vue";
import SessionTable from "./components/SessionTable.vue";
import {
  apiGet,
  autostartDisable,
  autostartEnable,
  autostartIsEnabled,
  clearConnection,
  errText,
  ping,
  setConnection,
} from "./api.js";
import { fmtDuration, fmtTs, normalizeBaseUrl } from "./format.js";
import { loadSettings, saveSettings } from "./settings.js";
import { connectMessage, statusView } from "./status.js";

const STORE_KEY = "netmon.profiles.v1";
const CHART_KEY = "netmon.chartMode";
const pageSize = 20;
const appWindow = getCurrentWindow();

const nav = ref("accounts");
const navItems = [
  { key: "dash", icon: "📊", label: "仪表盘" },
  { key: "accounts", icon: "👤", label: "账号管理" },
  { key: "logs", icon: "📋", label: "日志" },
];

const settings = reactive(loadSettings(localStorage));
const theme = ref(settings.theme === "light" ? "light" : "dark");
const chartMode = ref(loadChartMode());
const settingsOpen = ref(false);
const bgInput = ref(null);

const form = reactive({ name: "", base: "", token: "" });
const profiles = ref(loadProfiles());
const connecting = ref(false);
const connected = ref(false);
const showToken = ref(false);
const uiLog = ref([]);
const banner = reactive({ show: false, kind: "busy", text: "" });
const now = reactive({});
const history = ref([]);
const sessRows = ref([]);
const sessTotal = ref(0);
const sessPage = ref(1);
const sessLoading = ref(false);
const sessFilter = reactive({ ip: "", proto: "" });

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

function loadChartMode() {
  try {
    const m = localStorage.getItem(CHART_KEY);
    return ["line", "area", "bar"].includes(m) ? m : "line";
  } catch {
    return "line";
  }
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
function persistSettings() {
  saveSettings(localStorage, { ...settings });
}

function applyView() {
  document.documentElement.style.setProperty("--alpha", String(settings.opacity));
  if (settings.background) {
    document.documentElement.classList.add("has-bg");
    document.documentElement.style.setProperty("--bg-img", `url("${settings.background}")`);
  } else {
    document.documentElement.classList.remove("has-bg");
    document.documentElement.style.removeProperty("--bg-img");
  }
  persistSettings();
}

function setTheme(t) {
  theme.value = t === "light" ? "light" : "dark";
  settings.theme = theme.value;
  document.documentElement.setAttribute("data-theme", theme.value);
  persistSettings();
  pushLog("主题切换：" + (theme.value === "light" ? "明亮模式" : "夜间模式"));
}

function pickBackground() {
  if (bgInput.value) bgInput.value.click();
}
async function onBgFile(e) {
  const file = e.target.files && e.target.files[0];
  e.target.value = "";
  if (!file) return;
  try {
    const dataUrl = await downscaleImage(file, 1920, 0.82);
    settings.background = dataUrl;
    applyView();
    pushLog("已设置自定义背景图片");
  } catch (err) {
    pushLog("背景图片处理失败：" + errText(err));
  }
}
function clearBackground() {
  settings.background = "";
  applyView();
  pushLog("已恢复默认背景");
}
function downscaleImage(file, maxWidth, quality) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      try {
        const scale = Math.min(1, maxWidth / img.width);
        const w = Math.max(1, Math.round(img.width * scale));
        const h = Math.max(1, Math.round(img.height * scale));
        const canvas = document.createElement("canvas");
        canvas.width = w;
        canvas.height = h;
        canvas.getContext("2d").drawImage(img, 0, 0, w, h);
        URL.revokeObjectURL(url);
        resolve(canvas.toDataURL("image/jpeg", quality));
      } catch (err) {
        reject(err);
      }
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("图片读取失败"));
    };
    img.src = url;
  });
}

async function toggleAutostart() {
  try {
    if (settings.autostart) {
      await autostartDisable();
      settings.autostart = false;
    } else {
      await autostartEnable();
      settings.autostart = true;
    }
    persistSettings();
    pushLog(settings.autostart ? "已开启开机自启动" : "已关闭开机自启动");
  } catch (err) {
    pushLog("开机自启动设置失败：" + errText(err));
  }
}

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
  pushLog(`已保存连接：${item.name}`);
}
function useProfile(p) {
  form.name = p.name;
  form.base = p.base;
  form.token = p.token || "";
  setBanner("ok", `已载入连接：${p.name}（编辑中）`);
}
async function switchProfile(p) {
  useProfile(p);
  if (connected.value) await disconnect();
  pushLog(`切换连接：${p.name} → ${p.base}`);
  await connect();
}
function deleteProfile(name) {
  profiles.value = profiles.value.filter((p) => p.name !== name);
  persistProfiles();
  pushLog(`已删除连接：${name}`);
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
    pushLog("调用内核：set_connection");
    await withTimeout(setConnection(norm.url, form.token), 10000, "初始化连接");
    pushLog("内核 set_connection 完成");
    pushLog("请求实时数据 /api/v1/traffic/now");
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
    // 忽略清理错误
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
    // 轮询失败统一处理
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
  pushLog("MeTD 启动，进行 IPC 自检…");
  applyView();
  document.documentElement.setAttribute("data-theme", theme.value);
  try {
    await withTimeout(ping(), 3000, "IPC 自检");
    ipcOk.value = true;
    pushLog("IPC 自检通过");
  } catch (e) {
    ipcOk.value = false;
    pushLog("IPC 自检失败：" + errText(e));
  }
  try {
    settings.autostart = Boolean(await withTimeout(autostartIsEnabled(), 3000, "自启状态查询"));
  } catch {
    settings.autostart = false;
  }
  appWindow.onCloseRequested(async (event) => {
    if (settings.closeToTray) {
      event.preventDefault();
      await appWindow.hide();
    }
  });
  if (settings.startMinimized) {
    setTimeout(() => {
      appWindow.hide().catch(() => {});
    }, 1500);
  }
});

onBeforeUnmount(() => {
  stopTimers();
  if (toastTimer) clearTimeout(toastTimer);
});
</script>

<style scoped>
.layout { display: flex; height: 100%; gap: 12px; padding: 12px; }
.rail {
  width: 56px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: rgb(var(--panel-rgb) / var(--alpha, 1));
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 10px 7px;
  overflow: hidden;
  transition: width 0.22s ease;
}
.rail:hover { width: 168px; }
.rail-grow { flex: 1; }
.rail-btn {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  background: transparent;
  border: none;
  border-radius: 9px;
  padding: 9px 8px;
  color: var(--muted);
  font-size: 13px;
  text-align: left;
  white-space: nowrap;
}
.rail-btn:hover { background: rgb(var(--panel2-rgb) / var(--alpha, 1)); }
.rail-btn.active { background: rgb(var(--panel2-rgb) / var(--alpha, 1)); color: var(--accent); }
.rail-icon { font-size: 21px; flex-shrink: 0; transition: transform 0.15s ease; }
.rail-btn:hover .rail-icon { transform: scale(1.3); }
.rail-txt { opacity: 0; transition: opacity 0.18s ease 0.08s; }
.rail:hover .rail-txt { opacity: 1; }
.side {
  width: 330px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow: auto;
}
.panel-title { font-weight: 700; font-size: 15px; }
.hint { font-size: 12px; }
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 12px; overflow: auto; }
.field { display: flex; flex-direction: column; gap: 4px; }
.field label { color: var(--muted); font-size: 12px; }
.token-row { display: flex; gap: 6px; }
.token-row input { flex: 1; }
.eye { flex-shrink: 0; }
.row { display: flex; gap: 6px; }
.grow { flex: 1; }
.banner { padding: 8px 10px; border-radius: 7px; font-size: 12px; word-break: break-all; }
.banner-busy { background: #12314a; color: #bae6fd; border: 1px solid #0e7490; }
.banner-ok { background: #064e3b; color: #d1fae5; border: 1px solid #10b981; }
.banner-err { background: #450a0a; color: #fee2e2; border: 1px solid #ef4444; }
.section-title { font-size: 12px; margin-bottom: 6px; }
.empty { font-size: 12px; }
.profile { display: flex; align-items: center; gap: 5px; margin-bottom: 6px; }
.profile-main {
  flex: 1;
  min-width: 0;
  text-align: left;
  display: flex;
  align-items: center;
  gap: 7px;
  background: rgb(var(--panel2-rgb) / var(--alpha, 1));
  border: 1px solid var(--line);
  color: var(--text);
  border-radius: 6px;
  padding: 7px 8px;
  font-size: 13px;
}
.profile-main:hover { border-color: var(--accent); }
.profile-main.active { border-color: var(--ok); }
.profile-name { font-weight: 600; }
.profile-addr { font-size: 11px; overflow: hidden; text-overflow: ellipsis; }
.btn.small { padding: 4px 8px; font-size: 12px; }
.profile-del { background: none; border: none; color: var(--muted); font-size: 17px; padding: 2px 6px; }
.profile-del:hover { color: var(--danger); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: #475569; flex-shrink: 0; }
.dot.on { background: var(--ok); box-shadow: 0 0 6px var(--ok); }
.dot.big { width: 9px; height: 9px; }
.dot.ok { background: var(--ok); }
.dot.bad { background: var(--danger); }
.status-line { display: flex; align-items: center; gap: 7px; font-size: 13px; }
.logbox { overflow: auto; }
.log-line {
  font-family: Consolas, monospace;
  font-size: 11px;
  color: var(--muted);
  padding: 2px 0;
  border-bottom: 1px dashed var(--line);
  word-break: break-all;
}
.k-ok { color: var(--ok); }
.k-bad { color: var(--danger); }
.info-line { display: flex; gap: 16px; font-size: 12px; flex-wrap: wrap; }
.sess-head { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; }
.sess-title { font-weight: 700; margin-right: auto; }
.filter-ip { width: 170px; }
.welcome { max-width: 560px; margin: auto; text-align: center; padding: 30px; }
.welcome h2 { margin: 0 0 10px; }
.small { font-size: 12px; }
.overlay {
  position: fixed;
  inset: 0;
  z-index: 90;
  background: rgba(2, 6, 16, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
}
.dialog {
  width: 420px;
  max-height: 86vh;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.45);
}
.dialog-head { display: flex; justify-content: space-between; align-items: center; font-weight: 700; font-size: 16px; }
.seg { display: flex; gap: 6px; }
.seg-btn {
  flex: 1;
  background: var(--panel2);
  border: 1px solid var(--line);
  color: var(--muted);
  border-radius: 6px;
  padding: 6px 8px;
  font-size: 12px;
  transition: all 0.2s;
}
.seg-btn.active { background: var(--accent); border-color: var(--accent); color: #06222b; font-weight: 600; }
.switch-row { display: flex; justify-content: space-between; align-items: center; font-size: 13px; }
.switch {
  width: 42px;
  height: 22px;
  border-radius: 12px;
  border: 1px solid var(--line);
  background: var(--panel2);
  position: relative;
  transition: background 0.2s;
}
.switch::after {
  content: "";
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--muted);
  transition: left 0.2s, background 0.2s;
}
.switch.on { background: var(--ok); border-color: var(--ok); }
.switch.on::after { left: 22px; background: #fff; }
.hidden { display: none; }
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