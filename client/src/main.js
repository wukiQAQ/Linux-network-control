import { createApp } from "vue";
import App from "./App.vue";
import "./style.css";

// 启动前先套用保存的主题，避免闪烁
function loadTheme() {
  try {
    const t = localStorage.getItem("netmon.theme");
    return t === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}
document.documentElement.setAttribute("data-theme", loadTheme());

createApp(App).mount("#app");