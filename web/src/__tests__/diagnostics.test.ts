import { diagnosticsAPI, type DiagnosticsResponse } from "../services/diagnostics";
import { diagnostics, openDiagnostics, refreshDiagnostics, resetDiagnosticsFilters, setDiagnostics, setDiagnosticsFilter } from "../store/diagnostics";
import { appSettings, closeSettingsModal, openProviderModal, openSettingsModal, setAppSettings } from "../store/settings";
import { setWorkspace, workspace } from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Diagnostics", () => {
  it("encodes filters and uses the same-origin API for all actions", async () => {
    const original = globalThis.fetch;
    const calls: Array<{ url: string; method?: string }> = [];
    globalThis.fetch = async (url, init) => {
      calls.push({ url: String(url), method: init?.method });
      return new Response(JSON.stringify({ entries: [], directory: "/logs" }));
    };
    try {
      await diagnosticsAPI.list({ kind: "request", level: "ERROR", sessionID: "session /1", providerID: "a&b", q: " timeout ", });
      const url = new URL(calls[0].url, "http://localhost");
      assertEqual(url.searchParams.get("sessionID"), "session /1");
      assertEqual(url.searchParams.get("providerID"), "a&b");
      assertEqual(url.searchParams.get("q"), "timeout");
      assertEqual(url.searchParams.get("limit"), "200");
      assertEqual(url.searchParams.get("kind"), "request");
      assertEqual(url.searchParams.get("level"), "ERROR");
      await diagnosticsAPI.export();
      await diagnosticsAPI.openDirectory();
      assertEqual(calls[1].url, "/api/hoxi/diagnostics/export");
      assertEqual(calls[2].url, "/api/hoxi/diagnostics/open-directory");
      assertEqual(calls[2].method, "POST");
    } finally { globalThis.fetch = original; }
  });

  it("opens scoped diagnostics without retaining the previous scope or provider overlay", () => {
    const settings = { modalOpen: appSettings.modalOpen, activeTab: appSettings.activeTab };
    const providerModalOpen = workspace.providerModalOpen;
    try {
      openProviderModal("custom", "ep-1");
      assertEqual(appSettings.modalOpen, true);
      assertEqual(appSettings.activeTab, "custom");
      assertEqual(workspace.editingProviderID, "ep-1");
      openSettingsModal("providers");
      assertEqual(appSettings.activeTab, "providers");
      assertEqual(workspace.editingProviderID, null);
      closeSettingsModal();
      assertEqual(appSettings.modalOpen, false);
      assertEqual(workspace.providerModalOpen, false);

      setWorkspace("providerModalOpen", true);
      openDiagnostics({ providerID: "provider-a" });
      assertEqual(workspace.providerModalOpen, false);
      assertEqual(appSettings.activeTab, "errorLog");
      assertEqual(appSettings.modalOpen, true);
      assertEqual(diagnostics.filters.providerID, "provider-a");
      assertEqual(diagnostics.filters.kind, "request");
      setDiagnosticsFilter("q", "old search");
      openDiagnostics({ sessionID: "session-b" });
      assertEqual(diagnostics.filters.sessionID, "session-b");
      assertEqual(diagnostics.filters.providerID, "");
      assertEqual(diagnostics.filters.q, "");
      assertEqual(diagnostics.filters.kind, "");
    } finally {
      setAppSettings(settings);
      setWorkspace("providerModalOpen", providerModalOpen);
      resetDiagnosticsFilters();
    }
  });

  it("ignores stale reads and exposes partial and network failures", async () => {
    const original = diagnosticsAPI.list;
    let resolve!: (value: DiagnosticsResponse) => void;
    try {
      diagnosticsAPI.list = () => new Promise((done) => { resolve = done; });
      const oldRead = refreshDiagnostics();
      setDiagnosticsFilter("sessionID", "new-session");
      resolve({ entries: [], directory: "/old" });
      await oldRead;
      assertEqual(diagnostics.loaded, false);
      diagnosticsAPI.list = async () => ({ entries: [], directory: "/logs", error: "partial read" });
      await refreshDiagnostics();
      assertEqual(diagnostics.directory, "/logs");
      assertEqual(diagnostics.error, "partial read");
      diagnosticsAPI.list = async () => { throw new Error("offline"); };
      await refreshDiagnostics();
      assertTrue(diagnostics.error.includes("offline"));
      assertEqual(diagnostics.loading, false);
    } finally {
      diagnosticsAPI.list = original;
      resetDiagnosticsFilters();
      setDiagnostics("directory", "");
    }
  });
});
