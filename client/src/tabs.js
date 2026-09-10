// 应用内多开标签页的纯逻辑（便于单元测试）

export const TABS_KEY = "metd.tabs.v1";

export function newTabId() {
  return "ws-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 7);
}

export function emptySnapshot() {
  return {
    now: {},
    history: [],
    sessions: [],
    total: 0,
    page: 1,
    connected: false,
    logs: [],
    banner: { show: false, kind: "busy", text: "" },
    firing: [],
  };
}

export function createTab(id, title) {
  return {
    id: id || newTabId(),
    title: title || "新工作区",
    name: "",
    base: "",
    token: "",
    snapshot: emptySnapshot(),
  };
}

export function normalizeTabs(raw) {
  if (!Array.isArray(raw) || raw.length === 0) {
    return [createTab(newTabId(), "工作区 1")];
  }
  return raw.map((t, i) => {
    const src = t && typeof t === "object" ? t : {};
    return {
      id: typeof src.id === "string" && src.id ? src.id : newTabId(),
      title: typeof src.title === "string" && src.title ? src.title : "工作区 " + (i + 1),
      name: String(src.name || ""),
      base: String(src.base || ""),
      token: String(src.token || ""),
      snapshot: { ...emptySnapshot(), ...(src.snapshot || {}) },
    };
  });
}

export function closeTabById(list, id) {
  if (!Array.isArray(list) || list.length <= 1) return list;
  return list.filter((t) => t.id !== id);
}

export function nextActiveId(list, closedId, currentId) {
  if (currentId !== closedId) return currentId;
  return list && list.length ? list[0].id : "";
}