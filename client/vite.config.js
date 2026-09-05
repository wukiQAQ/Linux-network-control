import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// Tauri 开发时固定端口，生产构建产物输出到 dist/，由 src-tauri 内嵌。
export default defineConfig({
  plugins: [vue()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    target: "chrome105",
  },
});