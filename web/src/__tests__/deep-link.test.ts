import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { DESKTOP_INTENT_EVENT, onDesktopIntent } from "../services/desktop-events";
import {
  collectPendingApprovalRoots,
  collectPendingSessionIDs,
} from "../components/layout/left-nav-helpers";
import {
  appSettings,
  appLinkConfirmation,
  applyDesktopIntent,
  clearPendingDeepLink,
  closeHandoffModal,
  closeSettingsModal,
  focusFirstAttentionSession,
  leftNavOpen,
  openSessionDeepLink,
  pendingDeepLink,
  resolvePendingDeepLink,
  resolveAppLinkConfirmation,
  setDefaultWorkspaceDir,
  setLeftNavOpen,
  setSurfaceMode,
  setWorkspace,
  surfaceMode,
  workspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

describe("session deep links", () => {
  it("drops a pending deep link when the project changes or TTL expires", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [],
      });
      openSessionDeepLink({ sessionID: "ses_missing" });
      assertEqual(pendingDeepLink()?.sessionID, "ses_missing");

      // Switching to an unrelated project clears the pending link so it never fires later.
      setWorkspace("rootDir", "/repo-b");
      resolvePendingDeepLink();
      assertEqual(pendingDeepLink(), null);

      // Expired links are cleared immediately on re-evaluation.
      openSessionDeepLink({ sessionID: "ses_expired" }, { ttlMs: -1 });
      assertEqual(pendingDeepLink(), null);
    } finally {
      clearPendingDeepLink();
      setWorkspace(reconcile(saved));
    }
  });

  it("handles status bar menu desktop intents for attention, running, policy, and handoff", () => {
    const saved = structuredClone(unwrap(workspace));
    const savedNav = leftNavOpen();
    try {
      setLeftNavOpen(false);
      setWorkspace({
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sidebarProjectMode: "multi",
        sessions: [],
        pendingPermissions: [
          {
            id: "perm_1",
            sessionID: "ses_pending_1",
            permission: "bash",
            patterns: ["git push"],
            metadata: {},
            always: [],
          },
        ],
      });

      // Attention intent opens sidebar, preserves the current sidebar mode, and locates first pending approval
      applyDesktopIntent({ action: "attention" });
      assertEqual(leftNavOpen(), true);
      assertEqual(workspace.sidebarProjectMode, "multi");
      assertEqual(pendingDeepLink()?.sessionID, "ses_pending_1");

      // When multiple sessions have pending approvals and the first is already active, locating again cycles to the next
      setWorkspace({
        activeSessionID: "ses_pending_1",
        pendingPermissions: [
          {
            id: "perm_1",
            sessionID: "ses_pending_1",
            permission: "bash",
            patterns: ["git push"],
            metadata: {},
            always: [],
          },
          {
            id: "perm_2",
            sessionID: "ses_pending_2",
            permission: "bash",
            patterns: ["rm -rf dist"],
            metadata: {},
            always: [],
          },
        ],
      });
      focusFirstAttentionSession();
      assertEqual(workspace.sidebarProjectMode, "multi");
      assertEqual(pendingDeepLink()?.sessionID, "ses_pending_2");

      // Policy intent opens settings modal on the policy tab
      applyDesktopIntent({ action: "policy" });
      assertEqual(appSettings.modalOpen, true);
      assertEqual(appSettings.activeTab, "policy");

      // Handoff intent closes settings modal and opens handoff modal
      applyDesktopIntent({ action: "handoff" });
      assertEqual(appSettings.modalOpen, false);
      assertEqual(workspace.handoffModalOpen, true);
    } finally {
      clearPendingDeepLink();
      closeSettingsModal();
      closeHandoffModal();
      setLeftNavOpen(savedNav);
      setWorkspace(reconcile(saved));
    }
  });

  it("opens app consent separately and never navigates a browser link without confirmation", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      applyDesktopIntent({ action: "connect-app", requestID: "con_123" });
      assertEqual(appSettings.activeTab, "app-grants");
      setWorkspace({ rootDir: "/repo-a", inspectedBrowserURL: null });
      applyDesktopIntent({ action: "open-browser", params: { url: "https://example.com" } });
      assertEqual(Boolean(appLinkConfirmation()), true);
      assertEqual(workspace.inspectedBrowserURL, null);
      setWorkspace("rootDir", "/repo-b");
      resolveAppLinkConfirmation(true);
      assertEqual(workspace.inspectedBrowserURL, null);
    } finally {
      resolveAppLinkConfirmation(false);
      closeSettingsModal();
      setWorkspace(reconcile(saved));
    }
  });

  it("subscribes to both Wails runtime and native DOM desktop-intent wake-ups", () => {
    const g = globalThis as unknown as { window?: Record<string, unknown> };
    const prevWindow = g.window;
    const domListeners = new Map<string, Set<() => void>>();
    let wailsListener = null as (() => void) | null;
    let wailsOffCalls = 0;

    g.window = {
      runtime: {
        EventsOn: (name: string, cb: () => void) => {
          if (name === DESKTOP_INTENT_EVENT) {
            wailsListener = cb;
          }
          return () => {
            wailsOffCalls++;
            if (wailsListener === cb) wailsListener = null;
          };
        },
      },
      addEventListener: (name: string, cb: () => void) => {
        if (!domListeners.has(name)) domListeners.set(name, new Set());
        domListeners.get(name)!.add(cb);
      },
      removeEventListener: (name: string, cb: () => void) => {
        domListeners.get(name)?.delete(cb);
      },
    };

    try {
      let wakeups = 0;
      const dispose = onDesktopIntent(() => {
        wakeups++;
      });

      wailsListener?.();
      assertEqual(wakeups, 1);

      for (const cb of domListeners.get(DESKTOP_INTENT_EVENT) ?? []) cb();
      assertEqual(wakeups, 2);

      for (const cb of domListeners.get("focus") ?? []) cb();
      assertEqual(wakeups, 3);

      dispose();
      assertEqual(wailsOffCalls, 1);
      assertEqual(domListeners.get(DESKTOP_INTENT_EVENT)?.size ?? 0, 0);
      assertEqual(domListeners.get("focus")?.size ?? 0, 0);
    } finally {
      if (prevWindow === undefined) {
        delete g.window;
      } else {
        g.window = prevWindow;
      }
    }
  });

  it("isolates Work pending approvals from Code sidebar counts and switches surface on cross-surface deep links", async () => {
    const saved = structuredClone(unwrap(workspace));
    const savedNav = leftNavOpen();
    const prevSurface = surfaceMode();
    const origAPI = { ...hoxiAPI };
    try {
      setDefaultWorkspaceDir("/Users/test/.hoxi/workspaces/default");
      setSurfaceMode("code");
      setWorkspace({
        rootDir: "/repo-code",
        activeProjectID: "ws_code",
        bootstrappingSessions: false,
        activeSessionID: "ses_code_active",
        sessions: [
          {
            id: "ses_code_active",
            title: "Code Active",
            directory: "/repo-code",
            surface: "code",
            time: { created: 1, updated: 1 },
          },
        ],
        sessionIndex: {
          ses_code_active: { directory: "/repo-code", surface: "code", updated: 1 },
        },
        pendingPermissions: [
          {
            id: "per_work_1",
            sessionID: "ses_work_pending",
            directory: "/Users/test/.hoxi/workspaces/default",
            surface: "work",
            permission: "hoxi_work_publish",
            patterns: ["Publish draft"],
            metadata: {},
            always: [],
          },
        ],
        pendingQuestions: [
          {
            id: "que_work_1",
            sessionID: "ses_work_q",
            directory: "/Users/test/.hoxi/workspaces/default",
            surface: "work",
            questions: [],
          },
        ],
      });

      // Code sidebar helpers must exclude Work approvals/questions so Code never shows a ghost badge.
      assertEqual(collectPendingApprovalRoots("code").size, 0);
      assertEqual(collectPendingSessionIDs("code").size, 0);
      assertEqual(collectPendingApprovalRoots("work").size, 2);
      assertDeepEqual(Array.from(collectPendingApprovalRoots("work")).sort(), [
        "ses_work_pending",
        "ses_work_q",
      ]);

      // Code-scoped banner click must not navigate to Work approvals.
      focusFirstAttentionSession(undefined, undefined, { surface: "code" });
      assertEqual(pendingDeepLink(), null);
      assertEqual(surfaceMode(), "code");
      assertEqual(workspace.rootDir, "/repo-code");

      // Global attention intent (e.g. macOS status bar) targeting a Work approval switches surface atomically.
      hoxiAPI.listSessions = async (dir) =>
        dir === "/Users/test/.hoxi/workspaces/default"
          ? [
              {
                id: "ses_work_pending",
                title: "Work Approval",
                directory: "/Users/test/.hoxi/workspaces/default",
                surface: "work",
                time: { created: 2, updated: 2 },
              },
            ]
          : [];
      hoxiAPI.resolveProject = async (dir) => ({ projectID: dir, directory: dir, roots: [dir] });
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.listPermissions = async () => [];
      hoxiAPI.listQuestions = async () => [];
      hoxiAPI.getSessionStatuses = async () => ({});
      hoxiAPI.listAgents = async () => [];
      hoxiAPI.getSessionDiff = async () => [];

      applyDesktopIntent({ action: "attention" });
      await new Promise((r) => setTimeout(r, 30));

      assertEqual(surfaceMode(), "work");
      assertEqual(workspace.rootDir, "/Users/test/.hoxi/workspaces/default");
      assertEqual(workspace.activeSessionID, "ses_work_pending");

      // Navigating to a Code question from Work switches back to Code using question.directory.
      setWorkspace("pendingPermissions", []);
      setWorkspace("pendingQuestions", [
        {
          id: "que_code_1",
          sessionID: "ses_code_q",
          directory: "/repo-code",
          surface: "code",
          questions: [],
        },
      ]);
      hoxiAPI.listSessions = async (dir) =>
        dir === "/repo-code"
          ? [
              {
                id: "ses_code_q",
                title: "Code Question",
                directory: "/repo-code",
                surface: "code",
                time: { created: 3, updated: 3 },
              },
            ]
          : [];

      focusFirstAttentionSession();
      await new Promise((r) => setTimeout(r, 30));

      assertEqual(surfaceMode(), "code");
      assertEqual(workspace.rootDir, "/repo-code");
      assertEqual(workspace.activeSessionID, "ses_code_q");
    } finally {
      clearPendingDeepLink();
      Object.assign(hoxiAPI, origAPI);
      setSurfaceMode(prevSurface);
      setLeftNavOpen(savedNav);
      setWorkspace(reconcile(saved));
    }
  });
});
