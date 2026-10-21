import { reconcile, unwrap } from "solid-js/store";
import { buildMessageLink } from "../components/timeline/session-export";
import {
  clearPendingDeepLink,
  openSessionDeepLink,
  pendingDeepLink,
  resolvePendingDeepLink,
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
});
