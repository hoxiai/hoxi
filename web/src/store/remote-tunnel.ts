import { createStore } from "solid-js/store";
import { t } from "../i18n";
import {
  remoteTunnelAPI,
  type RemoteRelayQr,
  type RemoteTunnelPairRequest,
  type RemoteTunnelPending,
  type RemoteTunnelRequestPairRequest,
  type RemoteTunnelStatus,
} from "../services/remote-tunnel";

export const [remoteTunnel, setRemoteTunnel] = createStore<{
  status: RemoteTunnelStatus;
  relayQr: RemoteRelayQr | null;
  loaded: boolean;
  busy: boolean;
  error: string;
}>({
  status: { enabled: false, state: "disabled" },
  relayQr: null,
  loaded: false,
  busy: false,
  error: "",
});

// requestJSON throws "HTTP <status>: <body>"; surface the gateway's {"error"} text.
export function isTunnelLoopbackHost(hostname: string): boolean {
  const host = hostname.trim().toLowerCase().replace(/^\[|\]$/g, "");
  return host === "localhost" || host === "127.0.0.1" || host === "::1";
}

export function validateControllerUrl(value: string): string | null {
  const trimmed = value.trim().replace(/\/+$/, "");
  if (!trimmed) return t("remoteTunnel.errInvalidUrl");
  if (!/^https?:\/\//i.test(trimmed)) {
    return t("remoteTunnel.errScheme");
  }
  try {
    const parsed = new URL(trimmed);
    if (!parsed.hostname) {
      return t("remoteTunnel.errInvalidUrl");
    }
    if (parsed.protocol === "http:" && !isTunnelLoopbackHost(parsed.hostname)) {
      return t("remoteTunnel.errHttpsRequired");
    }
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return t("remoteTunnel.errScheme");
    }
  } catch {
    return t("remoteTunnel.errInvalidUrl");
  }
  return null;
}

export function localizeTunnelError(raw: string, fallback: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return fallback;
  const lower = trimmed.toLowerCase();
  if (lower.includes("public gopanel must use https") || lower.includes("must use https")) {
    return t("remoteTunnel.errHttpsRequired");
  }
  if (lower.includes("must start with https://")) {
    return t("remoteTunnel.errScheme");
  }
  if (lower.includes("url is invalid")) {
    return t("remoteTunnel.errInvalidUrl");
  }
  if (lower.includes("32-character")) {
    return t("remoteTunnel.errCodeInvalid");
  }
  if (lower.includes("remote tunnel is not available")) {
    return t("remoteTunnel.errUnavailable");
  }
  if (lower.startsWith("connect gopanel:")) {
    const reason = trimmed.replace(/^connect gopanel:\s*/i, "");
    return t("remoteTunnel.errConnectFailed", { reason });
  }
  return trimmed;
}

function errorText(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : "";
  const body = message.replace(/^HTTP \d+:\s*/, "");
  try {
    const parsed = JSON.parse(body) as { error?: unknown };
    if (typeof parsed.error === "string" && parsed.error.trim()) {
      return localizeTunnelError(parsed.error, fallback);
    }
  } catch {
    /* not JSON */
  }
  return localizeTunnelError(message, fallback);
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

// Loads the desktop-side relay QR the phone scans. The backend returns
// available=false until the desktop is paired, so callers can refresh freely.
export async function loadRelayQr(): Promise<void> {
  try {
    const qr = await remoteTunnelAPI.getRelayQr();
    setRemoteTunnel("relayQr", qr);
  } catch {
    setRemoteTunnel("relayQr", null);
  }
}

export async function requestRemoteTunnel(req: RemoteTunnelRequestPairRequest): Promise<boolean> {
  if (remoteTunnel.busy) return false;
  const validationError = validateControllerUrl(req.controllerUrl);
  if (validationError) {
    setRemoteTunnel("error", validationError);
    return false;
  }
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
  const validationError = validateControllerUrl(req.controllerUrl);
  if (validationError) {
    setRemoteTunnel("error", validationError);
    return false;
  }
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
