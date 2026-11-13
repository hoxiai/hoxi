// Runs a real Solid DOM regression in an isolated headless Chrome profile.
import { build, createServer, preview } from "vite";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const entry = process.argv[2];
if (!entry || !existsSync(path.resolve(webRoot, entry))) throw new Error("Pass a browser test entry relative to web/");
const chromePath = process.env.CHROME_BIN || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
  process.env.PROGRAMFILES && path.join(process.env.PROGRAMFILES, "Google/Chrome/Application/chrome.exe"),
].find((candidate) => candidate && existsSync(candidate));
if (!chromePath) throw new Error("Chrome is required; set CHROME_BIN to its executable");
process.env.HOXI_DISABLE_WORK_EXT = "1";
const profile = await mkdtemp(path.join(tmpdir(), "hoxi-browser-test-"));
const production = process.argv.includes("--production");
const output = production ? await mkdtemp(path.join(tmpdir(), "hoxi-browser-build-")) : undefined;
const config = {
  configFile: path.join(webRoot, "vite.config.ts"), root: webRoot,
  server: { host: "127.0.0.1", port: 0, open: false },
  plugins: [{
    name: "browser-test-entry",
    configureServer(vite) {
      vite.middlewares.use("/__browser_test", async (_req, res) => {
        const html = `<html><body><script type="module">
          import { runBrowserTest } from ${JSON.stringify(`/${entry}`)};
          window.browserTestResult = await runBrowserTest().then(
            results => ({ results }), error => ({ error: error.stack || String(error) })
          );
        </script></body></html>`;
        res.setHeader("Content-Type", "text/html");
        res.end(await vite.transformIndexHtml("/__browser_test", html));
      });
    },
    configurePreviewServer(vite) {
      vite.middlewares.use("/__browser_test", (_req, res) => {
        res.setHeader("Content-Type", "text/html");
        res.end(`<html><body><script type="module">
          import { runBrowserTest } from "/browser-test.js";
          window.browserTestResult = await runBrowserTest().then(
            results => ({ results }), error => ({ error: error.stack || String(error) })
          );
        </script></body></html>`);
      });
    },
  }],
};
let server;
let chrome;
let socket;
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
try {
  if (production) {
    await build({ ...config, logLevel: "warn", build: {
      outDir: output, emptyOutDir: true,
      lib: { entry: path.resolve(webRoot, entry), formats: ["es"], fileName: () => "browser-test.js" },
    } });
    server = await preview({ ...config, build: { outDir: output }, preview: { host: "127.0.0.1", port: 0, open: false } });
  } else {
    server = await createServer(config);
    await server.listen();
  }
  const address = server.httpServer.address();
  chrome = spawn(chromePath, [
    "--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank",
  ], { stdio: "ignore" });
  let launchError;
  chrome.on("error", (error) => { launchError = error; });
  let debugPort;
  for (let attempt = 0; attempt < 100 && !debugPort; attempt++) {
    if (launchError) throw launchError;
    try { debugPort = (await readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; } catch {}
    if (!debugPort) await delay(100);
  }
  if (!debugPort) throw new Error("Chrome did not start within 10 seconds");
  const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json();
  socket = new WebSocket(targets.find((target) => target.type === "page").webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let nextID = 0;
  const pending = new Map();
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    message.error ? request.reject(message.error) : request.resolve(message.result);
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++nextID;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params }));
  });
  await call("Page.navigate", { url: `http://127.0.0.1:${address.port}/__browser_test` });
  let result;
  for (let attempt = 0; attempt < 300 && !result; attempt++) {
    const response = await call("Runtime.evaluate", { expression: "window.browserTestResult", returnByValue: true });
    result = response.result?.value;
    if (!result) await delay(100);
  }
  if (!result) throw new Error("Browser test timed out after 30 seconds (check entry imports)");
  if (result.error) throw new Error(result.error);
  for (const message of result.results) console.log(`  ✓ ${message}`);
  console.log(`[browser-test] ${result.results.length} scenarios passed`);
} finally {
  socket?.close();
  if (chrome && chrome.exitCode === null) {
    const exited = new Promise((resolve) => chrome.once("exit", resolve));
    chrome.kill();
    await Promise.race([exited, delay(2000)]);
  }
  if (production && server) await new Promise((resolve) => server.httpServer.close(resolve));
  else await server?.close();
  await rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 });
  if (output) await rm(output, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 });
}
