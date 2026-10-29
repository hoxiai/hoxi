import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { computerAPI, type ComputerApp, type ComputerGrant, type ComputerPermissions, type ComputerScope, type ComputerSnapshot } from "../services/computer";
import { workspace } from "./workspace-state";
import { sessionDirectory } from "./session-query";
import { locale, t } from "../i18n";

export async function stopAllComputerControl(): Promise<void> { await computerAPI.stop(AbortSignal.timeout(5000)); }

export function createComputerController() {
  const [permissions, setPermissions] = createSignal<ComputerPermissions | null>(null);
  const [apps, setApps] = createSignal<ComputerApp[]>([]);
  const [grant, setGrant] = createSignal<ComputerGrant | null>(null);
  const [snapshot, setSnapshot] = createSignal<ComputerSnapshot | null>(null);
  const [selected, setSelected] = createSignal("");
  const [windowID, setWindowID] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const scope = createMemo<ComputerScope | null>(() => {
    const sessionID = workspace.activeSessionID, directory = sessionID ? sessionDirectory(sessionID) : undefined;
    return sessionID && directory ? { sessionID, directory } : null;
  }, null, { equals: (a, b) => a?.sessionID === b?.sessionID && a?.directory === b?.directory });
  let current: { abort: AbortController; scope: ComputerScope | null; revision: number } | null = null;
  const valid = (connection: typeof current) => Boolean(connection && current === connection && !connection.abort.signal.aborted &&
    scope()?.sessionID === connection.scope?.sessionID && scope()?.directory === connection.scope?.directory);
  function report(err: unknown) { setError(err instanceof Error ? err.message : String(err)); }

  createEffect(() => {
    const active = scope(), enabled = workspace.inspectorOpen && workspace.inspectorTab === "computer";
    setGrant(null); setSnapshot(null); setWindowID(""); setSelected(""); setBusy(false); setError("");
    if (!enabled) return;
    const connection = { scope: active, abort: new AbortController(), revision: 0 };
    current = connection;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      const revision = connection.revision;
      try {
        const [permission, state] = await Promise.all([computerAPI.permissions(connection.abort.signal), active ? computerAPI.state(active, connection.abort.signal) : Promise.resolve({ grant: null })]);
        if (valid(connection) && connection.revision === revision) {
          setPermissions(permission); setGrant(state.grant);
          if (!state.grant || state.grant.paused) setSnapshot(null);
        }
      } catch (err) { if (valid(connection)) report(err); }
      finally { if (valid(connection)) timer = setTimeout(poll, 2000); }
    }
    untrack(() => { void poll(); void refreshApps(); });
    onCleanup(() => { clearTimeout(timer); connection.abort.abort(); if (current === connection) current = null; });
  });

  async function refreshApps() {
    const connection = current;
    if (!connection || !valid(connection)) return;
    try {
      const result = await computerAPI.apps(connection.abort.signal);
      if (valid(connection)) setApps(result.apps);
    } catch (err) { if (valid(connection)) report(err); }
  }
  async function permission(kind: "screen" | "accessibility") {
    const connection = current;
    if (!connection || !valid(connection) || busy()) return;
    setBusy(true); setError("");
    try { const next = await computerAPI.requestPermission(kind, connection.abort.signal); if (valid(connection)) setPermissions(next); }
    catch (err) { if (valid(connection)) report(err); }
    finally { if (valid(connection)) setBusy(false); }
  }
  async function authorize() {
    const connection = current;
    if (!connection?.scope || !valid(connection) || !selected() || busy()) return;
    const appID = selected();
    setBusy(true); setError(""); const revision = ++connection.revision;
    try {
      const next = await computerAPI.grant(connection.scope, appID, locale(), connection.abort.signal);
      if (valid(connection) && connection.revision === revision) { setGrant(next.grant); setSnapshot(null); setWindowID(""); }
    } catch (err) { if (valid(connection) && connection.revision === revision) report(err); }
    finally { if (valid(connection) && connection.revision === revision) setBusy(false); }
  }
  async function updateAccess(action: "pause" | "revoke" | "stop") {
    const connection = current;
    if (!connection || !valid(connection)) return;
    const revision = ++connection.revision; setError(""); setSnapshot(null); setBusy(false);
    try {
      if (action === "stop") { await stopAllComputerControl(); if (valid(connection) && connection.revision === revision) setGrant(null); }
      else if (connection.scope) {
        const next = action === "revoke" ? await computerAPI.revoke(connection.scope, connection.abort.signal) : await computerAPI.pause(connection.scope, !grant()?.paused, connection.abort.signal);
        if (valid(connection) && connection.revision === revision) setGrant(next.grant);
      }
    } catch (err) { if (valid(connection) && connection.revision === revision) report(err); }
  }
  async function inspect() {
    const connection = current, target = grant();
    if (!connection?.scope || !valid(connection) || !target || target.paused || busy()) return;
    const appID = target.app.id, requestedWindow = windowID(), revision = connection.revision;
    setBusy(true); setError("");
    try {
      const next = await computerAPI.observe(connection.scope, requestedWindow, connection.abort.signal);
      if (valid(connection) && connection.revision === revision && grant()?.app.id === appID && !grant()?.paused && windowID() === requestedWindow) {
        setSnapshot(next); if (!requestedWindow) setWindowID(next.windowID);
      }
    } catch (err) { if (valid(connection) && connection.revision === revision) report(err); }
    finally { if (valid(connection) && connection.revision === revision) setBusy(false); }
  }
  function errorMessage() {
    const raw = error();
    if (!raw) return "";
    const known = {
      system_permissions_required: "computer.errorSystemPermissionsRequired",
      target_app_not_allowed: "computer.errorTargetAppNotAllowed",
      app_owned_by_another_session: "computer.errorAppOwnedByAnotherSession",
      app_permission_not_granted: "computer.errorAppPermissionNotGranted",
      native_desktop_app_confirmation_required: "computer.errorNativeDesktopAppConfirmationRequired",
      screen_recording_permission_required: "computer.errorScreenRecordingPermissionRequired",
      accessibility_permission_required: "computer.errorAccessibilityPermissionRequired",
      no_visible_target_window: "computer.errorNoVisibleTargetWindow",
      window_not_capturable: "computer.errorWindowNotCapturable",
      screen_capture_unavailable: "computer.errorScreenCaptureUnavailable",
      accessibility_inspection_unavailable: "computer.errorAccessibilityInspectionUnavailable",
    } as const;
    const code = (Object.keys(known) as Array<keyof typeof known>).find(value => raw.includes(value));
    return code ? t(known[code]) : raw;
  }
  return { scope, permissions, apps, grant, snapshot, selected, setSelected, windowID,
    setWindowID: (value: string) => { setWindowID(value); setSnapshot(null); void inspect(); }, busy, error: errorMessage,
    refreshApps, permission, authorize, updateAccess, inspect };
}
