// Local model-call log. Each row is one request this computer sent; the detail
// panel shows the metadata captured for that call and nothing from the prompt.
import { createSignal } from "solid-js";
import { createStore, reconcile } from "solid-js/store";
import { hoxiAPI, type RequestLogContent, type RequestLogEntry } from "../services/api";

// A store (not a signal) so a refresh reconciles by message id: untouched rows
// keep their identity and are not re-created in the table.
const [requestLog, setRequestLog] = createStore<RequestLogEntry[]>([]);
const [requestLogTotal, setRequestLogTotal] = createSignal(0);
const [requestLogLoading, setRequestLogLoading] = createSignal(false);
const [requestLogError, setRequestLogError] = createSignal("");

let generation = 0;
let activeNonSilent = 0;

export { requestLog, requestLogTotal, requestLogLoading, requestLogError };

// silent skips the loading flag so background refreshes do not flash the view.
export async function loadRequestLog(opts: { silent?: boolean } = {}) {
  const request = ++generation;
  if (!opts.silent) {
    activeNonSilent++;
    setRequestLogLoading(true);
  }
  setRequestLogError("");
  try {
    const res = await hoxiAPI.getRequestLog(500);
    if (request !== generation) return;
    const entries = Array.isArray(res?.entries) ? res.entries : [];
    // Unchanged rows keep their identity, so nothing re-renders for them.
    setRequestLog(reconcile(entries, { key: "id" }));
    setRequestLogTotal(res?.total || 0);
  } catch (err: any) {
    if (request !== generation) return;
    if (!opts.silent) {
      setRequestLog(reconcile([], { key: "id" }));
    }
    setRequestLogError(String(err?.message || err || ""));
  } finally {
    if (!opts.silent) {
      activeNonSilent = Math.max(0, activeNonSilent - 1);
      setRequestLogLoading(activeNonSilent > 0);
    }
  }
}

// On-demand input/output for one logged call, read from the local store.
export function loadRequestContent(id: string, sessionID: string): Promise<RequestLogContent> {
  return hoxiAPI.getRequestLogContent(id, sessionID);
}
