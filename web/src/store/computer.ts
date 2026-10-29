import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import {
  computerAPI,
  type ComputerApp,
  type ComputerElement,
  type ComputerGrant,
  type ComputerPermissionAction,
  type ComputerPermissions,
  type ComputerRect,
  type ComputerScope,
  type ComputerSnapshot,
  type ComputerWindow,
} from "../services/computer";
import { workspace } from "./workspace-state";
import { sessionDirectory } from "./session-query";
import { newIsolatedSession } from "./session-sandbox";
import { locale, t } from "../i18n";

export async function stopAllComputerControl(): Promise<void> {
  await computerAPI.stop(AbortSignal.timeout(5000));
}

export function resolveElementWindowBounds(
  element: ComputerElement,
  windowBounds?: ComputerRect
): ComputerRect | null {
  if (element.windowBounds && element.windowBounds.width > 0 && element.windowBounds.height > 0) {
    return element.windowBounds;
  }
  if (!windowBounds || element.bounds.width <= 0 || element.bounds.height <= 0) return null;
  return {
    x: element.bounds.x - windowBounds.x,
    y: element.bounds.y - windowBounds.y,
    width: element.bounds.width,
    height: element.bounds.height,
  };
}

export function createComputerController() {
  const [permissions, setPermissions] = createSignal<ComputerPermissions | null>(null);
  const [apps, setApps] = createSignal<ComputerApp[]>([]);
  const [grant, setGrant] = createSignal<ComputerGrant | null>(null);
  const [snapshot, setSnapshot] = createSignal<ComputerSnapshot | null>(null);
  const [selected, setSelected] = createSignal("");
  const [windowID, setWindowID] = createSignal("");
  const [appFilter, setAppFilter] = createSignal("");
  const [elementFilter, setElementFilter] = createSignal("");
  const [hoveredElementID, setHoveredElementID] = createSignal<number | null>(null);
  const [cursorPoint, setCursorPoint] = createSignal<{ x: number; y: number } | null>(null);
  const [copiedValue, setCopiedValue] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const scope = createMemo<ComputerScope | null>(() => {
    const sessionID = workspace.activeSessionID,
      directory = sessionID ? sessionDirectory(sessionID) : undefined;
    return sessionID && directory ? { sessionID, directory } : null;
  }, null, { equals: (a, b) => a?.sessionID === b?.sessionID && a?.directory === b?.directory });
  const canAuthorize = createMemo(() => Boolean(scope() || workspace.rootDir));
  let current: { abort: AbortController; scope: ComputerScope | null; revision: number } | null = null;
  let copiedTimer: ReturnType<typeof setTimeout> | undefined;
  const valid = (connection: typeof current) =>
    Boolean(
      connection &&
        current === connection &&
        !connection.abort.signal.aborted &&
        scope()?.sessionID === connection.scope?.sessionID &&
        scope()?.directory === connection.scope?.directory
    );
  function report(err: unknown) {
    setError(err instanceof Error ? err.message : String(err));
  }

  createEffect(() => {
    const active = scope(),
      enabled = workspace.inspectorOpen && workspace.inspectorTab === "computer";
    setGrant(null);
    setSnapshot(null);
    setWindowID("");
    setElementFilter("");
    setHoveredElementID(null);
    setCursorPoint(null);
    setBusy(false);
    setError("");
    if (!enabled) return;
    const connection = { scope: active, abort: new AbortController(), revision: 0 };
    current = connection;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function poll() {
      const revision = connection.revision;
      try {
        const [permission, state] = await Promise.all([
          computerAPI.permissions(connection.abort.signal),
          active ? computerAPI.state(active, connection.abort.signal) : Promise.resolve({ grant: null }),
        ]);
        if (valid(connection) && connection.revision === revision) {
          setPermissions(permission);
          setGrant(state.grant);
          if (!state.grant || state.grant.paused) setSnapshot(null);
        }
      } catch (err) {
        if (valid(connection)) report(err);
      } finally {
        if (valid(connection)) timer = setTimeout(poll, 2000);
      }
    }
    untrack(() => {
      void poll();
      void refreshApps();
    });
    onCleanup(() => {
      clearTimeout(timer);
      clearTimeout(copiedTimer);
      connection.abort.abort();
      if (current === connection) current = null;
    });
  });

  const filteredApps = createMemo(() => {
    const q = appFilter().trim().toLowerCase();
    if (!q) return apps();
    return apps().filter(
      (app) =>
        app.name.toLowerCase().includes(q) ||
        app.bundleID.toLowerCase().includes(q) ||
        app.path.toLowerCase().includes(q) ||
        String(app.pid).includes(q)
    );
  });

  const activeWindow = createMemo<ComputerWindow | null>(() => {
    const snap = snapshot();
    if (!snap || !snap.windows.length) return null;
    const id = windowID() || snap.windowID;
    return snap.windows.find((w) => w.id === id) ?? snap.windows[0] ?? null;
  });

  const filteredElements = createMemo<ComputerElement[]>(() => {
    const list = snapshot()?.elements ?? [];
    const q = elementFilter().trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (el) =>
        `#${el.id}` === q ||
        String(el.id) === q ||
        el.role.toLowerCase().includes(q) ||
        (el.name && el.name.toLowerCase().includes(q)) ||
        (el.value && el.value.toLowerCase().includes(q))
    );
  });

  const hoveredElement = createMemo<ComputerElement | null>(() => {
    const id = hoveredElementID();
    if (id === null) return null;
    return snapshot()?.elements.find((el) => el.id === id) ?? null;
  });

  function copyToClipboard(text: string) {
    if (!text) return;
    setCopiedValue(text);
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => setCopiedValue(""), 1800);
    try {
      void navigator.clipboard?.writeText(text);
    } catch {
      /* ignore */
    }
  }

  async function refreshApps() {
    const connection = current;
    if (!connection || !valid(connection)) return;
    try {
      const result = await computerAPI.apps(connection.abort.signal);
      if (valid(connection)) setApps(result.apps);
    } catch (err) {
      if (valid(connection)) report(err);
    }
  }
  async function permission(kind: ComputerPermissionAction) {
    const connection = current;
    if (!connection || !valid(connection) || busy()) return;
    setBusy(true);
    setError("");
    try {
      const next = await computerAPI.requestPermission(kind, connection.abort.signal);
      if (valid(connection)) setPermissions(next);
    } catch (err) {
      if (valid(connection)) report(err);
    } finally {
      if (valid(connection)) setBusy(false);
    }
  }
  async function authorize() {
    const appID = selected();
    if (!appID || busy()) return;
    let connection = current;
    if (!connection || !valid(connection)) return;
    if (!connection.scope) {
      if (!workspace.rootDir) return;
      setBusy(true);
      setError("");
      try {
        await newIsolatedSession();
      } catch (err) {
        setBusy(false);
        report(err);
        return;
      }
      connection = current;
      if (!connection?.scope || !valid(connection)) {
        setBusy(false);
        return;
      }
    }
    setBusy(true);
    setError("");
    const revision = ++connection.revision;
    try {
      const [next, perm] = await Promise.all([
        computerAPI.grant(connection.scope, appID, locale(), connection.abort.signal),
        computerAPI.permissions(connection.abort.signal).catch(() => null),
      ]);
      if (valid(connection) && connection.revision === revision) {
        if (perm) setPermissions(perm);
        setGrant(next.grant);
        setSnapshot(null);
        setWindowID("");
      }
    } catch (err) {
      if (valid(connection) && connection.revision === revision) report(err);
    } finally {
      if (valid(connection) && connection.revision === revision) setBusy(false);
    }
  }
  async function updateAccess(action: "pause" | "revoke" | "stop") {
    const connection = current;
    if (!connection || !valid(connection)) return;
    const revision = ++connection.revision;
    setError("");
    setSnapshot(null);
    setBusy(false);
    try {
      if (action === "stop") {
        await stopAllComputerControl();
        if (valid(connection) && connection.revision === revision) setGrant(null);
      } else if (connection.scope) {
        const next =
          action === "revoke"
            ? await computerAPI.revoke(connection.scope, connection.abort.signal)
            : await computerAPI.pause(connection.scope, !grant()?.paused, connection.abort.signal);
        if (valid(connection) && connection.revision === revision) setGrant(next.grant);
      }
    } catch (err) {
      if (valid(connection) && connection.revision === revision) report(err);
    }
  }
  async function inspect(withImage = true) {
    const connection = current,
      target = grant();
    if (!connection?.scope || !valid(connection) || !target || target.paused || busy()) return;
    const appID = target.app.id,
      requestedWindow = windowID(),
      revision = connection.revision;
    setBusy(true);
    setError("");
    try {
      const next = await computerAPI.observe(
        connection.scope,
        requestedWindow,
        connection.abort.signal,
        withImage
      );
      if (
        valid(connection) &&
        connection.revision === revision &&
        grant()?.app.id === appID &&
        !grant()?.paused &&
        windowID() === requestedWindow
      ) {
        const prev = snapshot();
        const merged: ComputerSnapshot =
          !withImage && prev && prev.windowID === next.windowID && prev.image
            ? { ...next, image: prev.image, width: prev.width, height: prev.height }
            : next;
        setSnapshot(merged);
        if (!requestedWindow) setWindowID(next.windowID);
      }
    } catch (err) {
      if (valid(connection) && connection.revision === revision) {
        report(err);
        const msg = err instanceof Error ? err.message : String(err);
        if (permissions()?.platform === "darwin") {
          if (msg.includes("accessibility_permission_required")) {
            void computerAPI
              .requestPermission("accessibility", connection.abort.signal)
              .then((p) => {
                if (valid(connection)) setPermissions(p);
              })
              .catch(() => {});
          } else if (msg.includes("screen_recording_permission_required")) {
            void computerAPI
              .requestPermission("screen", connection.abort.signal)
              .then((p) => {
                if (valid(connection)) setPermissions(p);
              })
              .catch(() => {});
          }
        }
      }
    } finally {
      if (valid(connection) && connection.revision === revision) setBusy(false);
    }
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
      computer_ui_origin_required: "computer.errorUiOriginRequired",
      computer_use_requires_registered_session: "computer.errorRegisteredSessionRequired",
    } as const;
    const code = (Object.keys(known) as Array<keyof typeof known>).find((value) => raw.includes(value));
    return code ? t(known[code]) : raw;
  }
  return {
    scope,
    canAuthorize,
    permissions,
    apps,
    filteredApps,
    appFilter,
    setAppFilter,
    grant,
    snapshot,
    activeWindow,
    filteredElements,
    elementFilter,
    setElementFilter,
    hoveredElementID,
    setHoveredElementID,
    hoveredElement,
    cursorPoint,
    setCursorPoint,
    copiedValue,
    copyToClipboard,
    selected,
    setSelected,
    windowID,
    setWindowID: (value: string) => {
      setWindowID(value);
      setSnapshot(null);
      void inspect(true);
    },
    busy,
    error: errorMessage,
    refreshApps,
    permission,
    authorize,
    updateAccess,
    inspect,
  };
}
