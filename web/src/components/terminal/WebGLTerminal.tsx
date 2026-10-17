import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { LoaderCircle, RefreshCw } from "lucide-solid";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import { hoxiAPI } from "../../services/api";
import { activeDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";

function wsEndpoint(ptyID: string, directory?: string): string {
  const base =
    workspace.wsBaseURL ||
    `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}`;
  const dir = directory ? `?directory=${encodeURIComponent(directory)}` : "";
  return `${base}/pty/${ptyID}/connect${dir}`;
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
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  let teardown: (() => void) | null = null;

  const boot = async () => {
    teardown?.();
    setStatus("connecting");
    setErrorMsg(null);
    container.innerHTML = "";

    const term = new Terminal({
      fontFamily: '"JetBrains Mono", "SF Mono", Menlo, monospace',
      fontSize: 12,
      cursorBlink: true,
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

    const directory = activeDirectory();
    let socket: WebSocket | null = null;
    let ptyID: string | null = null;
    let disposed = false;

    const onData = term.onData((chunk) => {
      if (socket && socket.readyState === WebSocket.OPEN) socket.send(chunk);
    });

    const syncSize = () => {
      try {
        fit.fit();
      } catch {
        return;
      }
      if (socket && socket.readyState === WebSocket.OPEN && ptyID && term.cols && term.rows) {
        void hoxiAPI.resizePTY(ptyID, term.cols, term.rows, directory).catch(() => undefined);
      }
    };

    const resizeObserver = new ResizeObserver(() => syncSize());
    resizeObserver.observe(container);

    try {
      const pty = await hoxiAPI.createPTY(directory);
      ptyID = pty.id;
    } catch (err) {
      console.error("create PTY failed:", err);
      if (!disposed) {
        setStatus("error");
        setErrorMsg("Failed to create PTY session");
      }
    }

    if (ptyID && !disposed) {
      socket = new WebSocket(wsEndpoint(ptyID, directory));
      socket.onopen = () => {
        setStatus("ready");
        syncSize();
      };
      socket.onmessage = (evt) => term.write(decodeFrame(evt.data));
      socket.onerror = () => {
        if (!disposed) {
          setStatus("error");
          setErrorMsg("Terminal WebSocket error");
        }
      };
      socket.onclose = () => {
        if (!disposed) setStatus("connecting");
      };
    }

    teardown = () => {
      disposed = true;
      resizeObserver.disconnect();
      onData.dispose();
      try {
        socket?.close();
      } catch {
        /* noop */
      }
      term.dispose();
    };
  };

  onMount(() => {
    void boot();
    onCleanup(() => teardown?.());
  });

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
          onClick={() => void boot()}
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
              <span class="text-red-400">{errorMsg()}</span>
            </Show>
          </div>
        </Show>
      </div>
    </div>
  );
}
