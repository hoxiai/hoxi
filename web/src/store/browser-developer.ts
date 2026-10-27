import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { browserWorkflowAPI, type BrowserDiagnostic } from "../services/browser-workflows";
import type { BrowserScope } from "../services/browser";
import { hoxiAPI } from "../services/api";
import { setWorkspace } from "./workspace-state";
import { t } from "../i18n";

export type BrowserViewportMode = "fit" | "desktop" | "mobile";
export type BrowserPreviewScale = "fit" | "0.5" | "1" | "1.5";
export const browserViewportPresets = {
  desktop: { width: 1440, height: 900, deviceScaleFactor: 1 },
  mobile: { width: 390, height: 844, deviceScaleFactor: 1 },
};

interface Options {
  scope: () => BrowserScope | null;
  tab: () => string | undefined;
  frame: () => string | null;
  enabled: () => boolean;
  error: (message: string) => void;
}

export function createBrowserDeveloper(options: Options) {
  const [open, setOpen] = createSignal(false);
  const [viewportMode, setViewportMode] = createSignal<BrowserViewportMode>("fit");
  const [previewScale, setPreviewScale] = createSignal<BrowserPreviewScale>("fit");
  const [diagnostics, setDiagnostics] = createSignal<BrowserDiagnostic[]>([]);
  const [error, setError] = createSignal("");
  const [capturing, setCapturing] = createSignal(false);
  const [notice, setNotice] = createSignal("");
  let current: { scope: BrowserScope; tab: string; abort: AbortController; revision: number } | null = null;
  const selectedTab = createMemo(options.tab), active = createMemo(() => open() && options.enabled());
  const valid = (connection: typeof current) => Boolean(connection && current === connection && !connection.abort.signal.aborted &&
    options.scope()?.sessionID === connection.scope.sessionID && options.scope()?.directory === connection.scope.directory && options.tab() === connection.tab);
  createEffect(() => {
    const scope = options.scope(), tab = selectedTab(), enabled = active();
    setDiagnostics([]); setError(""); setCapturing(false); setNotice("");
    if (!scope || !tab || !enabled) return;
    const connection = { scope, tab, abort: new AbortController(), revision: 0 };
    current = connection;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      const revision = connection.revision;
      try {
        const result = await browserWorkflowAPI.diagnostics(scope!, connection.abort.signal);
        if (valid(connection) && connection.revision === revision) { setDiagnostics(result.diagnostics); setError(""); }
      } catch (err) { if (valid(connection)) setError(String(err)); }
      finally { if (valid(connection)) timer = setTimeout(poll, 1500); }
    }
    untrack(() => { void poll(); });
    onCleanup(() => { connection.abort.abort(); clearTimeout(timer); if (current === connection) current = null; });
  });

  async function clearDiagnostics() {
    const connection = current;
    if (!connection || !valid(connection)) return;
    const revision = ++connection.revision;
    try {
      const result = await browserWorkflowAPI.clearDiagnostics(connection.scope, connection.abort.signal);
      if (valid(connection) && connection.revision === revision) setDiagnostics(result.diagnostics);
    } catch (err) { if (valid(connection)) setError(String(err)); }
  }

  async function attachScreenshot() {
    const connection = current, image = options.frame();
    if (!connection || !valid(connection) || !image || capturing()) return;
    setCapturing(true); setNotice("");
    try {
      const result = await hoxiAPI.uploadAttachment({ ...connection.scope, filename: `browser-${Date.now()}.jpg`, mime: "image/jpeg", dataUrl: image });
      if (!valid(connection)) return;
      setWorkspace("composerDraftEditBySession", connection.scope.sessionID, previous => ({
        id: `browser-screenshot-${Date.now()}`, text: previous?.text || "", skillSelection: previous?.skillSelection,
        attachments: [...(previous?.attachments || []), { type: "file" as const, mime: result.mime, filename: result.filename, url: result.url }],
      }));
      setNotice(t("inspector.browser.screenshotAttached"));
    } catch (err) { if (valid(connection)) options.error(String(err)); }
    finally { if (valid(connection)) setCapturing(false); }
  }
  return { open, setOpen, viewportMode, setViewportMode, previewScale, setPreviewScale, diagnostics, error,
    capturing, notice, clearDiagnostics, attachScreenshot };
}
