// Local model-call log. Each row is one request this computer sent; the detail
// panel shows the metadata captured for that call and nothing from the prompt.
import { createSignal } from "solid-js";
import { hoxiAPI, type RequestLogEntry } from "../services/api";

const [requestLog, setRequestLog] = createSignal<RequestLogEntry[]>([]);
const [requestLogTotal, setRequestLogTotal] = createSignal(0);
const [requestLogLoading, setRequestLogLoading] = createSignal(false);
const [requestLogError, setRequestLogError] = createSignal("");

let generation = 0;

export { requestLog, requestLogTotal, requestLogLoading, requestLogError };

export async function loadRequestLog() {
  const request = ++generation;
  setRequestLogLoading(true);
  setRequestLogError("");
  try {
    const res = await hoxiAPI.getRequestLog(500);
    if (request !== generation) return;
    setRequestLog(Array.isArray(res?.entries) ? res.entries : []);
    setRequestLogTotal(res?.total || 0);
  } catch (err: any) {
    if (request !== generation) return;
    setRequestLog([]);
    setRequestLogError(String(err?.message || err || ""));
  } finally {
    if (request === generation) setRequestLogLoading(false);
  }
}
