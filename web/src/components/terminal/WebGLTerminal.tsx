import { createEffect, createSignal, on, onCleanup, Show } from "solid-js";
import { LoaderCircle, RefreshCw } from "lucide-solid";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import { hoxiAPI } from "../../services/api";
import { activeDirectory, appSettings, workspace } from "../../store/workspace";
import { t } from "../../i18n";
import { startTerminalConnection } from "./terminal-connection";

function wsEndpoint(ptyID: string, directory?: string): string {
  const base =
    workspace.wsBaseURL ||
    `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}`;
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : "";
  return `${base}/pty/${encodeURIComponent(ptyID)}/connect${dir}`;
}

// OpenCode streams raw PTY bytes; tolerate JSON-framed frames as well.
function decodeFrame(data: string | ArrayBuffer): string {
  if (typeof data !== "string") {
    return new TextDecoder().decode(data);
  }
  if (data.startsWith("{")) {
    try {
      const parsed = JSON.parse(data);
      const payload = parsed?.data ?? parsed?.output ?? parsed?.chunk;
      if (typeof payload === "string") return payload;
    } catch {
      /* raw payload */
    }
  }
  return data;
}

export default function WebGLTerminal() {
  let container!: HTMLDivElement;
  const [status, setStatus] = createSignal<"connecting" | "ready" | "error">("connecting");
  const [errorKey, setErrorKey] = createSignal<"error" | "disconnected">("error");
  const [retry, setRetry] = createSignal(0);
  let activeTerm: Terminal | null = null;
  let activeFit: FitAddon | null = null;

  createEffect(() => {
    const fz = appSettings.terminalFontSize;
    const blink = appSettings.terminalCursorBlink;
    if (activeTerm) {
      activeTerm.options.fontSize = fz;
      activeTerm.options.cursorBlink = blink;
      try {
        activeFit?.fit();
      } catch {
        /* ignore */
      }
    }
  });

  const boot = (directory: string | undefined) => {
    setStatus("connecting");
    setErrorKey("error");
    container.innerHTML = "";
    // Never let an unresolved session directory fall back to the server's cwd.
    if (!directory) {
      setStatus("error");
      setErrorKey("disconnected");
      return;
    }

    const term = new Terminal({
      fontFamily: '"JetBrains Mono", "SF Mono", Menlo, monospace',
      fontSize: appSettings.terminalFontSize,
      cursorBlink: appSettings.terminalCursorBlink,
      convertEol: true,
      theme: {
        background: "#0b0d12",
        foreground: "#e5e7eb",
        cursor: "#6366f1",
        selectionBackground: "#6366f155",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);
    activeTerm = term;
    activeFit = fit;
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      term.loadAddon(webgl);
    } catch {
      /* fall back to DOM renderer when WebGL is unavailable */
    }
    try {
      fit.fit();
    } catch {
      /* container not measurable yet */
    }

    const fitSize = () => {
      try {
        fit.fit();
      } catch {
        return { cols: 0, rows: 0 };
      }
      return { cols: term.cols, rows: term.rows };
    };

    let connection: ReturnType<typeof startTerminalConnection> | undefined;
    const onData = term.onData((chunk) => connection?.send(chunk));
    const resizeObserver = new ResizeObserver(() => {
      fitSize();
      connection?.resize();
    });
    connection = startTerminalConnection({
      directory,
      api: hoxiAPI,
      connect: (id, dir) => new WebSocket(wsEndpoint(id, dir)),
      size: fitSize,
      write: (data) => term.write(decodeFrame(data)),
      ready: () => setStatus("ready"),
      error: (reason) => { setErrorKey(reason); setStatus("error"); },
      disposeView: () => {
        resizeObserver.disconnect();
        onData.dispose();
        activeTerm = null;
        activeFit = null;
        term.dispose();
      },
    });
    onCleanup(connection.dispose);
    resizeObserver.observe(container);
  };

  createEffect(on(
    () => [workspace.activeSessionID, activeDirectory(), retry()] as const,
    ([, directory]) => boot(directory),
  ));

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center gap-2 px-3 py-2 border-b border-white/[0.07]">
        <span
          class={`inline-block w-1.5 h-1.5 rounded-full ${
            status() === "ready"
              ? "bg-emerald-400"
              : status() === "error"
                ? "bg-red-400"
                : "bg-amber-400 animate-pulse"
          }`}
        />
        <span class="text-[11px] text-gray-400">
          {status() === "ready"
            ? t("inspector.terminal.connected")
            : status() === "error"
              ? t("inspector.terminal.error")
              : t("inspector.terminal.connecting")}
        </span>
        <button
          class="ml-auto text-gray-500 hover:text-gray-200 cursor-pointer"
          onClick={() => setRetry((value) => value + 1)}
          title={t("inspector.terminal.reconnect")}
        >
          <RefreshCw size={13} />
        </button>
      </div>

      <div class="flex-1 min-h-0 relative bg-[#0b0d12]">
        <div ref={container} class="absolute inset-0 p-1" />
        <Show when={status() !== "ready"}>
          <div class="absolute inset-0 flex flex-col items-center justify-center gap-2 text-[11px] text-gray-500 pointer-events-none">
            <Show when={status() === "error"} fallback={<LoaderCircle size={16} class="animate-spin" />}>
              <span class="text-red-400">{t(`inspector.terminal.${errorKey()}`)}</span>
            </Show>
          </div>
        </Show>
      </div>
    </div>
  );
}
