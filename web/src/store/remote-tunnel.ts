import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { remoteTunnelAPI, type RemoteTunnelPairRequest, type RemoteTunnelStatus } from "../services/remote-tunnel";

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

export async function loadRemoteTunnelStatus(): Promise<void> {
  try {
    setRemoteTunnel({ status: await remoteTunnelAPI.getStatus(), loaded: true });
  } catch (err) {
    setRemoteTunnel({ loaded: true, error: errorText(err, t("remoteTunnel.loadError")) });
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
