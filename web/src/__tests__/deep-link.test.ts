import { reconcile, unwrap } from "solid-js/store";
import { buildMessageLink } from "../components/timeline/session-export";
import {
  appSettings,
  applyDesktopIntent,
  clearPendingDeepLink,
  closeHandoffModal,
  closeSettingsModal,
  leftNavOpen,
  openSessionDeepLink,
  pendingDeepLink,
  resolvePendingDeepLink,
  setLeftNavOpen,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

describe("session deep links", () => {
  it("builds an app-scheme link instead of a webview origin", () => {
    assertEqual(buildMessageLink("ses_abc123"), "hoxi://session/ses_abc123");
  });

  it("anchors a specific message", () => {
    assertEqual(
      buildMessageLink("ses_abc123", "msg_a_1792552422878_12"),
      "hoxi://session/ses_abc123?message=msg_a_1792552422878_12"
    );
  });

  it("escapes ids", () => {
    assertEqual(buildMessageLink("ses_a b", "msg/a"), "hoxi://session/ses_a%20b?message=msg%2Fa");
  });

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

      // Attention intent opens sidebar, switches to attention mode, and targets first pending approval
      applyDesktopIntent({ action: "attention" });
      assertEqual(leftNavOpen(), true);
      assertEqual(workspace.sidebarProjectMode, "attention");
      assertEqual(pendingDeepLink()?.sessionID, "ses_pending_1");

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
});
