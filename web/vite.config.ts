import { defineConfig } from "vite";
import solidPlugin from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";

const target = process.env.HOXI_BACKEND_URL || "http://127.0.0.1:5089";

export default defineConfig({
  plugins: [tailwindcss(), solidPlugin()],
  server: {
    port: 5173,
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
  build: {
    target: "esnext",
    outDir: "dist",
    emptyOutDir: true,
  },
});
