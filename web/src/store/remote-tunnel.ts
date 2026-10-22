import { createStore } from "solid-js/store";
import { t } from "../i18n";
import {
  remoteTunnelAPI,
  type RemoteTunnelPairRequest,
  type RemoteTunnelPending,
  type RemoteTunnelRequestPairRequest,
  type RemoteTunnelStatus,
} from "../services/remote-tunnel";

export const [remoteTunnel, setRemoteTunnel] = createStore<{
  status: RemoteTunnelStatus;
  loaded: boolean;
  busy: boolean;
  error: string;
}>({
  status: { enabled: false, state: "disabled" },
  loaded: false,
  busy: false,
  error: "",
});

// requestJSON throws "HTTP <status>: <body>"; surface the gateway's {"error"} text.
function errorText(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : "";
  const body = message.replace(/^HTTP \d+:\s*/, "");
  try {
    const parsed = JSON.parse(body) as { error?: unknown };
    if (typeof parsed.error === "string" && parsed.error.trim()) return parsed.error.trim();
  } catch {
    /* not JSON */
  }
  return fallback;
}

let remoteTunnelGen = 0;

/** The in-flight one-click request, if any, for the UI to render the code and countdown. */
export function remoteTunnelPending(): RemoteTunnelPending | undefined {
  return remoteTunnel.status.pending;
}

export async function loadRemoteTunnelStatus(): Promise<void> {
  const gen = ++remoteTunnelGen;
  try {
    const status = await remoteTunnelAPI.getStatus();
    if (gen !== remoteTunnelGen) return;
    setRemoteTunnel({ status, loaded: true, error: "" });
  } catch (err) {
    if (gen !== remoteTunnelGen) return;
    setRemoteTunnel({ loaded: true, error: errorText(err, t("remoteTunnel.loadError")) });
  }
}

export async function requestRemoteTunnel(req: RemoteTunnelRequestPairRequest): Promise<boolean> {
  if (remoteTunnel.busy) return false;
  ++remoteTunnelGen;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    const status = await remoteTunnelAPI.requestPair(req);
    ++remoteTunnelGen;
    setRemoteTunnel({ status, loaded: true, error: "" });
    return true;
  } catch (err) {
    ++remoteTunnelGen;
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.requestError")));
    return false;
  } finally {
    setRemoteTunnel("busy", false);
  }
}

export async function cancelRemoteTunnel(): Promise<void> {
  if (remoteTunnel.busy) return;
  ++remoteTunnelGen;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    const status = await remoteTunnelAPI.cancelPair();
    ++remoteTunnelGen;
    setRemoteTunnel({ status, loaded: true, error: "" });
  } catch (err) {
    ++remoteTunnelGen;
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.cancelError")));
  } finally {
    setRemoteTunnel("busy", false);
  }
}

export async function pairRemoteTunnel(req: RemoteTunnelPairRequest): Promise<boolean> {
  if (remoteTunnel.busy) return false;
  ++remoteTunnelGen;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    const status = await remoteTunnelAPI.pair(req);
    ++remoteTunnelGen;
    setRemoteTunnel({ status, loaded: true, error: "" });
    return true;
  } catch (err) {
    ++remoteTunnelGen;
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.pairError")));
    return false;
  } finally {
    setRemoteTunnel("busy", false);
  }
}

export async function disableRemoteTunnel(): Promise<void> {
  if (remoteTunnel.busy) return;
  ++remoteTunnelGen;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    const status = await remoteTunnelAPI.disable();
    ++remoteTunnelGen;
    setRemoteTunnel({ status, loaded: true, error: "" });
  } catch (err) {
    ++remoteTunnelGen;
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.disableError")));
  } finally {
    setRemoteTunnel("busy", false);
  }
}
