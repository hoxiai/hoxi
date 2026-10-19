import { defineConfig } from "vite";
import solidPlugin from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";

const isRelease = (process.env.HOXI_VARIANT || "").trim().toLowerCase() === "release";
const target =
  process.env.HOXI_BACKEND_URL || (isRelease ? "http://127.0.0.1:5410" : "http://127.0.0.1:5420");
const port = Number(process.env.HOXI_WEB_PORT) || (isRelease ? 5411 : 5421);

export default defineConfig({
  plugins: [tailwindcss(), solidPlugin()],
  server: {
    port,
    strictPort: false,
    proxy: {
      "/api": { target, changeOrigin: true, ws: true },
      "/healthz": { target, changeOrigin: true },
      "/session": { target, changeOrigin: true },
      "/event": { target, changeOrigin: true },
      "/pty": { target, changeOrigin: true, ws: true },
      "/file": { target, changeOrigin: true },
      "/find": { target, changeOrigin: true },
      "/worktree": { target, changeOrigin: true },
      "/permission": { target, changeOrigin: true },
      "/question": { target, changeOrigin: true },
      "/vcs": { target, changeOrigin: true },
      "/global": { target, changeOrigin: true },
      "/provider": { target, changeOrigin: true },
      "/auth": { target, changeOrigin: true },
      "/config": { target, changeOrigin: true },
      "/command": { target, changeOrigin: true },
      "/agent": { target, changeOrigin: true },
      "/lsp": { target, changeOrigin: true },
      "/mcp": { target, changeOrigin: true },
      "/formatter": { target, changeOrigin: true },
      "/skill": { target, changeOrigin: true },
      "/experimental": { target, changeOrigin: true },
      "/path": { target, changeOrigin: true },
      "/project": { target, changeOrigin: true },
    },
  },
  preview: {
    port: 5411,
    strictPort: false,
  },
  worker: {
    format: "es",
  },
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: true,
  },
});
