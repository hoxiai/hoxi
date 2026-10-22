import { hoxiAPI, type RequestLogEntry, type RequestLogResponse } from "../services/api";
import { diagnosticsAPI, type DiagnosticsResponse } from "../services/diagnostics";
import { diagnostics, openDiagnostics, refreshDiagnostics, resetDiagnosticsFilters, setDiagnostics, setDiagnosticsFilter } from "../store/diagnostics";
import { loadRequestLog, requestLog, requestLogError, requestLogLoading } from "../store/request-log";
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

  it("resets requestLogLoading when a non-silent load is superseded by a silent poll and preserves rows on silent error", async () => {
    const origGetRequestLog = hoxiAPI.getRequestLog;
    let resolveFirst!: (res: RequestLogResponse) => void;
    let resolveSecond!: (res: RequestLogResponse) => void;
    let call = 0;
    try {
      hoxiAPI.getRequestLog = () => {
        call++;
        if (call === 1) return new Promise<RequestLogResponse>((r) => { resolveFirst = r; });
        return new Promise<RequestLogResponse>((r) => { resolveSecond = r; });
      };

      const pendingEntry: RequestLogEntry = {
        id: "req-1",
        sessionID: "ses-1",
        time: 1000,
        durationMs: 0,
        providerID: "openai",
        modelID: "gpt-5",
        client: "local",
        level: "pending",
        inputTokens: 0,
        outputTokens: 0,
        reasoningTokens: 0,
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
      };

      const p1 = loadRequestLog();
      assertEqual(requestLogLoading(), true, "non-silent load sets loading=true");

      // Background silent poll supersedes p1 before p1 finishes
      const p2 = loadRequestLog({ silent: true });
      resolveSecond({ entries: [pendingEntry], total: 1, local: true, timezone: "UTC" });
      await p2;

      resolveFirst({ entries: [], total: 0, local: true, timezone: "UTC" });
      await p1;
      assertEqual(requestLogLoading(), false, "loading must unlock once non-silent request settles");
      assertEqual(requestLog.length, 1, "superseded non-silent load must not overwrite newer silent poll");

      // Reactive accessor over reconciled entry.level transitions from pending -> info
      const pending = () => requestLog[0]?.level === "pending";
      assertEqual(pending(), true);

      hoxiAPI.getRequestLog = async () => ({
        entries: [{ ...pendingEntry, level: "info", completed: 2000, durationMs: 1000 }],
        total: 1,
        local: true,
        timezone: "UTC",
      });
      await loadRequestLog({ silent: true });
      assertEqual(pending(), false, "reconciled entry updates reactive pending() accessor");

      // Silent background poll error must not wipe existing rows
      hoxiAPI.getRequestLog = async () => {
        throw new Error("transient poll error");
      };
      await loadRequestLog({ silent: true });
      assertEqual(requestLog.length, 1, "silent poll failure keeps existing rows");
      assertTrue(requestLogError().includes("transient poll error"));
    } finally {
      hoxiAPI.getRequestLog = origGetRequestLog;
    }
  });
});
