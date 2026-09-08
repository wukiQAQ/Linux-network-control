// 设置项的读写与取值纯函数（独立于 DOM，便于单元测试）

export const SETTINGS_KEY = "metd.settings.v1";

export const DEFAULT_SETTINGS = {
  theme: "dark", // dark | light
  opacity: 1, // 面板背景透明度 0.55 ~ 1
  background: "", // 自定义背景图 dataURL
  autostart: false,
  startMinimized: false,
  closeToTray: true,
};

export function clampOpacity(v) {
  const n = Number(v);
  if (!Number.isFinite(n)) return DEFAULT_SETTINGS.opacity;
  return Math.min(1, Math.max(0.55, n));
}

export function normalizeSettings(raw) {
  const src = raw && typeof raw === "object" ? raw : {};
  const s = { ...DEFAULT_SETTINGS };
  if (src.theme === "light" || src.theme === "dark") s.theme = src.theme;
  s.opacity = clampOpacity(src.opacity);
  s.background = typeof src.background === "string" ? src.background : "";
  s.autostart = Boolean(src.autostart);
  s.startMinimized = Boolean(src.startMinimized);
  s.closeToTray = src.closeToTray === undefined ? DEFAULT_SETTINGS.closeToTray : Boolean(src.closeToTray);
  return s;
}

export function loadSettings(storage) {
  try {
    const raw = storage ? storage.getItem(SETTINGS_KEY) : null;
    return normalizeSettings(raw ? JSON.parse(raw) : {});
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

export function saveSettings(storage, settings) {
  try {
    if (storage) storage.setItem(SETTINGS_KEY, JSON.stringify(settings));
    return true;
  } catch {
    return false;
  }
}