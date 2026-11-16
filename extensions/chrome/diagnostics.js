export function diagnosticURL(raw) {
  try {
    const url = new URL(raw);
    if (!["http:", "https:"].includes(url.protocol)) return "";
    url.username = url.password = url.search = url.hash = "";
    return url.href.slice(0, 1024);
  } catch { return ""; }
}

export function observeDiagnostics(link, method, event) {
  link.diagnostics ||= [];
  link.diagnosticRequests ||= new Map();
  const append = (kind, level, message, url = "") => {
    link.diagnosticSequence = (link.diagnosticSequence || 0) + 1;
    link.diagnostics.push({ id: link.diagnosticSequence, kind, level, message: message.slice(0, 2048), url: diagnosticURL(url), time: Date.now() });
    if (link.diagnostics.length > 100) link.diagnostics.shift();
  };
  if (method === "Runtime.consoleAPICalled" && ["error", "warning"].includes(event.type)) {
    const text = (event.args || []).slice(0, 8).map(arg => String(arg.type === "string" ? arg.value || "" : arg.description || "").slice(0, 512)).join(" ");
    append("console", event.type, text);
  } else if (method === "Runtime.exceptionThrown") {
    const detail = event.exceptionDetails || {};
    append("console", "error", `${detail.text || ""} ${detail.exception?.description || ""}`, detail.url);
  } else if (method === "Network.requestWillBeSent") {
    if (link.diagnosticRequests.size >= 256) link.diagnosticRequests.clear();
    link.diagnosticRequests.set(event.requestId, diagnosticURL(event.request?.url));
  } else if (method === "Network.responseReceived" && event.response?.status >= 400) {
    append("request", "error", `HTTP ${event.response.status}`, event.response.url);
  } else if (method === "Network.loadingFailed") {
    if (!event.canceled) append("request", "error", /\bnet::ERR_[A-Z0-9_]{1,80}\b/.exec(event.errorText)?.[0] || "request_failed", link.diagnosticRequests.get(event.requestId));
    link.diagnosticRequests.delete(event.requestId);
  } else if (method === "Network.loadingFinished") link.diagnosticRequests.delete(event.requestId);
}
