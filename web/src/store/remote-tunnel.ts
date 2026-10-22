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

/** The in-flight one-click request, if any, for the UI to render the code and countdown. */
export function remoteTunnelPending(): RemoteTunnelPending | undefined {
  return remoteTunnel.status.pending;
}

export async function loadRemoteTunnelStatus(): Promise<void> {
  try {
    setRemoteTunnel({ status: await remoteTunnelAPI.getStatus(), loaded: true });
  } catch (err) {
    setRemoteTunnel({ loaded: true, error: errorText(err, t("remoteTunnel.loadError")) });
  }
}

export async function requestRemoteTunnel(req: RemoteTunnelRequestPairRequest): Promise<boolean> {
  if (remoteTunnel.busy) return false;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    setRemoteTunnel("status", await remoteTunnelAPI.requestPair(req));
    return true;
  } catch (err) {
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.requestError")));
    return false;
  } finally {
    setRemoteTunnel("busy", false);
  }
}

export async function cancelRemoteTunnel(): Promise<void> {
  try {
    setRemoteTunnel("status", await remoteTunnelAPI.cancelPair());
  } catch (err) {
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.cancelError")));
  }
}

export async function pairRemoteTunnel(req: RemoteTunnelPairRequest): Promise<boolean> {
  if (remoteTunnel.busy) return false;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    setRemoteTunnel("status", await remoteTunnelAPI.pair(req));
    return true;
  } catch (err) {
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.pairError")));
    return false;
  } finally {
    setRemoteTunnel("busy", false);
  }
}

export async function disableRemoteTunnel(): Promise<void> {
  if (remoteTunnel.busy) return;
  setRemoteTunnel({ busy: true, error: "" });
  try {
    setRemoteTunnel("status", await remoteTunnelAPI.disable());
  } catch (err) {
    setRemoteTunnel("error", errorText(err, t("remoteTunnel.disableError")));
  } finally {
    setRemoteTunnel("busy", false);
  }
}
