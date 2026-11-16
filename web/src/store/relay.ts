import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { relayAPI, type RelayKeyInfo, type RelayStatus } from "../services/relay";

export const [localRelay, setLocalRelay] = createStore<{
  baseURL: string;
  chatURL: string;
  modelsURL: string;
  keys: RelayKeyInfo[];
  loaded: boolean;
  busy: boolean;
  error: string;
  createdToken: string;
}>({
  baseURL: "",
  chatURL: "",
  modelsURL: "",
  keys: [],
  loaded: false,
  busy: false,
  error: "",
  createdToken: "",
});

let loading: Promise<void> | undefined;

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

function applyStatus(status: RelayStatus) {
  setLocalRelay({
    baseURL: status.baseURL || "",
    chatURL: status.endpoints?.chatCompletions || "",
    modelsURL: status.endpoints?.models || "",
    keys: Array.isArray(status.keys) ? status.keys : [],
    loaded: true,
    error: "",
  });
}

export function loadLocalRelay(): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    try {
      applyStatus(await relayAPI.getStatus());
    } catch (err) {
      setLocalRelay({ loaded: true, error: errorText(err, t("relay.loadError")) });
    } finally {
      loading = undefined;
    }
  })();
  return loading;
}

/** Issues a key and keeps the plaintext token only in memory for one copy. */
export async function createLocalRelayKey(label: string): Promise<boolean> {
  if (localRelay.busy) return false;
  setLocalRelay({ busy: true, error: "", createdToken: "" });
  try {
    const created = await relayAPI.createKey(label.trim());
    setLocalRelay((prev) => ({
      keys: [created.key, ...prev.keys],
      createdToken: created.token,
    }));
    return true;
  } catch (err) {
    setLocalRelay({ error: errorText(err, t("relay.createError")) });
    return false;
  } finally {
    setLocalRelay("busy", false);
  }
}

export async function revokeLocalRelayKey(id: string): Promise<boolean> {
  if (localRelay.busy) return false;
  setLocalRelay({ busy: true, error: "" });
  try {
    const res = await relayAPI.revokeKey(id);
    setLocalRelay({ keys: Array.isArray(res.keys) ? res.keys : [] });
    return true;
  } catch (err) {
    setLocalRelay({ error: errorText(err, t("relay.revokeError")) });
    return false;
  } finally {
    setLocalRelay("busy", false);
  }
}

export function dismissCreatedRelayToken(): void {
  setLocalRelay("createdToken", "");
}
