import { createStore } from "solid-js/store";
import { diagnosticsAPI, type DiagnosticEntry, type DiagnosticsFilters } from "../services/diagnostics";
import { openSettingsModal } from "./settings";
import { setWorkspace } from "./workspace-state";

const emptyFilters = (): DiagnosticsFilters => ({ kind: "", level: "", sessionID: "", providerID: "", q: "" });

export const [diagnostics, setDiagnostics] = createStore({
  filters: emptyFilters(),
  entries: [] as DiagnosticEntry[],
  directory: "",
  loading: false,
  loaded: false,
  error: "",
  actionError: "",
  exporting: false,
  opening: false,
});

let generation = 0;

export function setDiagnosticsFilter<K extends keyof DiagnosticsFilters>(key: K, value: DiagnosticsFilters[K]) {
  // Invalidate an in-flight response before the debounced replacement starts.
  generation++;
  setDiagnostics("filters", key, value);
  setDiagnostics({ entries: [], loaded: false, loading: false, error: "" });
}

export function resetDiagnosticsFilters() {
  generation++;
  setDiagnostics({ filters: emptyFilters(), entries: [], loaded: false, loading: false, error: "" });
}

export function openDiagnostics(scope: { sessionID?: string; providerID?: string } = {}) {
  resetDiagnosticsFilters();
  setDiagnostics("filters", {
    ...emptyFilters(),
    sessionID: scope.sessionID || "",
    providerID: scope.providerID || "",
    kind: scope.providerID ? "request" : "",
  });
  setDiagnostics("actionError", "");
  setWorkspace("providerModalOpen", false);
  openSettingsModal(scope.sessionID || scope.providerID ? "errorLog" : "diagnostics");
}

export async function refreshDiagnostics() {
  const request = ++generation;
  const filters = { ...diagnostics.filters };
  setDiagnostics({ loading: true, error: "" });
  try {
    const result = await diagnosticsAPI.list(filters);
    if (request !== generation) return;
    setDiagnostics({ entries: result.entries, directory: result.directory, error: result.error || "", loaded: true });
  } catch (error) {
    if (request === generation) setDiagnostics("error", String(error));
  } finally {
    if (request === generation) setDiagnostics("loading", false);
  }
}

export async function exportDiagnostics(): Promise<Blob | undefined> {
  if (diagnostics.exporting) return;
  setDiagnostics({ exporting: true, actionError: "" });
  try {
    return await diagnosticsAPI.export();
  } catch (error) {
    setDiagnostics("actionError", String(error));
  } finally {
    setDiagnostics("exporting", false);
  }
}

export async function openDiagnosticsDirectory() {
  if (diagnostics.opening) return;
  setDiagnostics({ opening: true, actionError: "" });
  try {
    const result = await diagnosticsAPI.openDirectory();
    if (result?.error) throw new Error(result.error);
  } catch (error) {
    setDiagnostics("actionError", String(error));
  } finally {
    setDiagnostics("opening", false);
  }
}
