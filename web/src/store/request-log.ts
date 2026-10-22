// Local model-call log. Each row is one request this computer sent; the detail
// panel shows the metadata captured for that call and nothing from the prompt.
import { createSignal } from "solid-js";
import { hoxiAPI, type RequestLogContent, type RequestLogEntry } from "../services/api";

const [requestLog, setRequestLog] = createSignal<RequestLogEntry[]>([]);
const [requestLogTotal, setRequestLogTotal] = createSignal(0);
const [requestLogLoading, setRequestLogLoading] = createSignal(false);
const [requestLogError, setRequestLogError] = createSignal("");

let generation = 0;
let signature = "";

export { requestLog, requestLogTotal, requestLogLoading, requestLogError };

// On-demand input/output for one logged call, read from the local store.
export function loadRequestContent(id: string, sessionID: string): Promise<RequestLogContent> {
  return hoxiAPI.getRequestLogContent(id, sessionID);
}

// silent skips the loading flag so background refreshes do not flash the view,
// and an unchanged payload is dropped so the list does not re-render needlessly.
export async function loadRequestLog(opts: { silent?: boolean } = {}) {
  const request = ++generation;
  if (!opts.silent) setRequestLogLoading(true);
  setRequestLogError("");
  try {
    const res = await hoxiAPI.getRequestLog(500);
    if (request !== generation) return;
    const entries = Array.isArray(res?.entries) ? res.entries : [];
    const next = JSON.stringify(entries);
    if (next !== signature) {
      signature = next;
      setRequestLog(entries);
      setRequestLogTotal(res?.total || 0);
    }
  } catch (err: any) {
    if (request !== generation) return;
    signature = "";
    setRequestLog([]);
    setRequestLogError(String(err?.message || err || ""));
  } finally {
    if (!opts.silent && request === generation) setRequestLogLoading(false);
  }
}
