// web/src/store/cloudflared.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { cloudflaredAPI, type CloudflaredStatus } from "../services/cloudflared";
import { loadPairingCode } from "./pairing";

export const [cloudflared, setCloudflared] = createStore<{
  status: CloudflaredStatus;
  loaded: boolean;
  busy: boolean;
  error: string;
}>({
  status: { enabled: false, state: "disabled", hasToken: false },
  loaded: false,
  busy: false,
  error: "",
});

let cloudflaredGen = 0;

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

export async function loadCloudflaredStatus(): Promise<void> {
  const gen = ++cloudflaredGen;
  try {
    const status = await cloudflaredAPI.getStatus();
    if (gen !== cloudflaredGen) return;
    setCloudflared({ status, loaded: true, error: "" });
  } catch (err) {
    if (gen !== cloudflaredGen) return;
    setCloudflared({ loaded: true, error: errorText(err, t("cloudflared.loadError")) });
  }
}

export async function enableCloudflared(token: string, hostname: string): Promise<boolean> {
  if (cloudflared.busy) return false;
  ++cloudflaredGen;
  setCloudflared({ busy: true, error: "" });
  try {
    const status = await cloudflaredAPI.enable(token, hostname);
    ++cloudflaredGen;
    setCloudflared({ status, loaded: true, error: "" });
    void loadPairingCode();
    return true;
  } catch (err) {
    ++cloudflaredGen;
    setCloudflared("error", errorText(err, t("cloudflared.enableError")));
    return false;
  } finally {
    setCloudflared("busy", false);
  }
}

export async function disableCloudflared(): Promise<void> {
  if (cloudflared.busy) return;
  ++cloudflaredGen;
  setCloudflared({ busy: true, error: "" });
  try {
    const status = await cloudflaredAPI.disable();
    ++cloudflaredGen;
    setCloudflared({ status, loaded: true, error: "" });
    void loadPairingCode();
  } catch (err) {
    ++cloudflaredGen;
    setCloudflared("error", errorText(err, t("cloudflared.disableError")));
  } finally {
    setCloudflared("busy", false);
  }
}
