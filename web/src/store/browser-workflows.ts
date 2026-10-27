import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { browserWorkflowAPI, type BrowserWorkflows } from "../services/browser-workflows";
import type { BrowserAction, BrowserActionResult, BrowserScope } from "../services/browser";
import { t } from "../i18n";

interface Options {
  scope: () => BrowserScope | null;
  tab: () => string | undefined;
  enabled: () => boolean;
  request: (action: BrowserAction) => Promise<BrowserActionResult | undefined>;
  send: (action: BrowserAction) => void;
  error: (message: string) => void;
}

export function createBrowserWorkflows(options: Options) {
  const [state, setState] = createSignal<BrowserWorkflows>({ downloads: [] });
  const [busy, setBusy] = createSignal("");
  const [error, setError] = createSignal("");
  let current: { scope: BrowserScope; tab: string; abort: AbortController } | null = null;
  const selectedTab = createMemo(options.tab), enabled = createMemo(options.enabled);
  const valid = (connection: typeof current) => Boolean(connection && current === connection && !connection.abort.signal.aborted &&
    options.scope()?.sessionID === connection.scope.sessionID && options.scope()?.directory === connection.scope.directory && options.tab() === connection.tab);

  createEffect(() => {
    const scope = options.scope(), tab = selectedTab(), active = enabled();
    setState({ downloads: [] }); setBusy(""); setError("");
    if (!scope || !tab || !active) return;
    const connection = { scope, tab, abort: new AbortController() };
    current = connection;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      try {
        const next = await browserWorkflowAPI.state(connection.scope, connection.tab, connection.abort.signal);
        if (valid(connection)) { setState(next); setError(""); }
      } catch (err) { if (valid(connection)) setError(String(err)); }
      finally { if (valid(connection)) timer = setTimeout(poll, 750); }
    }
    untrack(() => { void poll(); });
    onCleanup(() => { connection.abort.abort(); clearTimeout(timer); if (current === connection) current = null; });
  });

  async function upload(files: File[]) {
    const connection = current, request = state().upload;
    if (!connection || !valid(connection) || !request || busy()) return;
    if (files.length < 1) return;
    if (files.length > 16 || (!request.multiple && files.length > 1) || files.reduce((size, file) => size + file.size, 0) > 50 * 1024 * 1024) {
      options.error(t("inspector.browser.uploadLimit")); return;
    }
    setBusy(request.id);
    try {
      const controlled = await options.request({ action: "control", manual: true });
      if (!valid(connection) || !controlled?.manual || state().upload?.id !== request.id) return;
      const next = await browserWorkflowAPI.upload(connection.scope, request.id, files, connection.abort.signal);
      if (valid(connection)) setState(next);
    } catch (err) { if (valid(connection)) options.error(String(err)); }
    finally { if (valid(connection)) setBusy(""); }
  }

  async function answer(accept: boolean, text: string) {
    const connection = current, dialog = state().dialog;
    if (!connection || !valid(connection) || !dialog || busy()) return;
    setBusy(dialog.id);
    // Queue ownership before answering; answering itself must bypass a page
    // command waiting for the dialog, so it can release that command.
    options.send({ action: "control", manual: true });
    try {
      const next = await browserWorkflowAPI.answer(connection.scope, connection.tab, dialog.id, accept, text, connection.abort.signal);
      if (valid(connection)) setState(next);
    } catch (err) { if (valid(connection)) options.error(String(err)); }
    finally { if (valid(connection)) setBusy(""); }
  }

  async function download(action: "save" | "open" | "reveal" | "cancel", id: string) {
    const connection = current;
    if (!connection || !valid(connection) || busy()) return;
    setBusy(id);
    try {
      const result = await browserWorkflowAPI.download(connection.scope, action, id, t("inspector.browser.saveDownload"), connection.abort.signal);
      if (valid(connection) && "downloads" in result) setState(result);
    } catch (err) { if (valid(connection)) options.error(String(err)); }
    finally { if (valid(connection)) setBusy(""); }
  }

  function cancelUpload() {
    const upload = state().upload;
    if (upload) options.send({ action: "upload_cancel", requestID: upload.id });
  }
  return { state, busy, error, upload, answer, download, cancelUpload };
}
