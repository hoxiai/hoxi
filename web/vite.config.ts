import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import solidPlugin from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";

const webRoot = fileURLToPath(new URL(".", import.meta.url));
const srcRoot = fileURLToPath(new URL("./src", import.meta.url));
const lucideSolidEsm = fileURLToPath(
  new URL("./node_modules/lucide-solid/dist/esm/lucide-solid.js", import.meta.url),
);
const resolveWorkRepoDir = () => {
  if (process.env.HOXI_WORK_DIR) {
    return path.resolve(webRoot, process.env.HOXI_WORK_DIR);
  }
  const candidates = [
    path.resolve(webRoot, "../../hoxi-work"),
    path.resolve(webRoot, "../../../../../hoxi-work"),
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(path.join(candidate, "web/index.tsx"))) {
      return candidate;
    }
  }
  return candidates[0];
};
const workRepoDir = resolveWorkRepoDir();
const workExtEntry = path.join(workRepoDir, "web/index.tsx");
const hasWorkExt =
  process.env.HOXI_DISABLE_WORK_EXT !== "1" && fs.existsSync(workExtEntry);

const isRelease = (process.env.HOXI_VARIANT || "").trim().toLowerCase() === "release";
const target =
  process.env.HOXI_BACKEND_URL || (isRelease ? "http://127.0.0.1:5410" : "http://127.0.0.1:5420");
const port = Number(process.env.HOXI_WEB_PORT) || (isRelease ? 5411 : 5421);

export default defineConfig({
  plugins: [tailwindcss(), solidPlugin()],
  resolve: {
    dedupe: ["solid-js", "solid-js/web", "solid-js/store", "lucide-solid"],
    alias: [
      { find: /^lucide-solid$/, replacement: lucideSolidEsm },
      {
        find: /^solid-js$/,
        replacement: fileURLToPath(
          new URL("./node_modules/solid-js/dist/solid.js", import.meta.url),
        ),
      },
      {
        find: /^solid-js\/web$/,
        replacement: fileURLToPath(
          new URL("./node_modules/solid-js/web/dist/web.js", import.meta.url),
        ),
      },
      {
        find: /^solid-js\/store$/,
        replacement: fileURLToPath(
          new URL("./node_modules/solid-js/store/dist/store.js", import.meta.url),
        ),
      },
      { find: /^@hoxi-core\/(.*)$/, replacement: `${srcRoot}/$1` },
      ...(hasWorkExt
        ? [
            { find: /^.*\/surface-extension(?:\.ts)?$/, replacement: workExtEntry },
          ]
        : []),
    ],
  },
  server: {
    fs: {
      allow: [webRoot, ...(hasWorkExt ? [workRepoDir] : [])],
    },
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
    chunkSizeWarningLimit: 900,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("node_modules/solid-js")) {
            return "vendor-solid";
          }
          if (id.includes("node_modules/lucide-solid")) {
            return "vendor-icons";
          }
          if (id.includes("node_modules/@pierre/diffs")) {
            return "vendor-pierre-diffs";
          }
          if (id.includes("node_modules/@shikijs") || id.includes("node_modules/shiki")) {
            if (!id.includes("/langs/") && !id.includes("/themes/")) {
              return "vendor-shiki-core";
            }
          }
        },
      },
    },
  },
});
