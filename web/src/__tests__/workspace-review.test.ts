import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, type CodeAuditRequest, type CodeAuditResult } from "../services/api";
import { runWorkspaceReview, setWorkspace, workspace } from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

async function withReviewFixture(
  options: { observer: boolean; directory: string | null },
  run: (fixture: { sessionID: string; calls: CodeAuditRequest[] }) => Promise<void>,
): Promise<void> {
  const saved = structuredClone(unwrap(workspace));
  const originalAudit = hoxiAPI.auditProviderCode;
  const sessionID = `ses_review_${Math.random().toString(36).slice(2)}`;
  const calls: CodeAuditRequest[] = [];
  hoxiAPI.auditProviderCode = async (req: CodeAuditRequest): Promise<CodeAuditResult> => {
    calls.push(req);
    return {
      ok: true,
      score: 88,
      pass: true,
      summary: "Looks good.",
      issues: [],
      observerProvider: "observer",
      observerModel: "obs-model",
    };
  };
  try {
    // With no observer and no worker model, nothing can be resolved and no request may be sent.
    setWorkspace(
      "defaultAI",
      "model",
      (options.observer ? { providerID: "worker", modelID: "worker-model" } : undefined) as never,
    );
    setWorkspace("observerModel", options.observer ? "observer/obs-model" : "");
    setWorkspace("globalConfig", reconcile({}) as never);
    setWorkspace("sessions", [
      {
        id: sessionID,
        title: "review fixture",
        directory: options.directory ?? undefined,
        time: { created: 0, updated: 0 },
      } as never,
    ]);
    await run({ sessionID, calls });
  } finally {
    hoxiAPI.auditProviderCode = originalAudit;
    setWorkspace(reconcile(saved) as never);
  }
}

describe("workspace review", () => {
  it("sends branch scope with the repo directory and stores the verdict per session", async () => {
    await withReviewFixture({ observer: true, directory: "/repo/.hoxi/worktrees/a" }, async ({ sessionID, calls }) => {
      const result = await runWorkspaceReview(sessionID, "branch", " release ");
      assertEqual(calls.length, 1, "exactly one observer call");
      assertEqual(calls[0].reviewScope, "branch");
      assertEqual(calls[0].repoDir, "/repo/.hoxi/worktrees/a");
      assertEqual(calls[0].baseRef, "release", "base branch must be trimmed");
      assertEqual(calls[0].diffs.length, 0, "backend collects diffs for workspace reviews");
      assertEqual(result?.scope, "branch");
      assertEqual(result?.baseRef, "release");
      assertEqual(workspace.reviewsBySession[sessionID]?.score, 88, "verdict must be keyed by session");
    });
  });

  it("uncommitted scope ignores any base branch", async () => {
    await withReviewFixture({ observer: true, directory: "/repo" }, async ({ sessionID, calls }) => {
      const result = await runWorkspaceReview(sessionID, "uncommitted", "release");
      assertEqual(calls[0].reviewScope, "uncommitted");
      assertEqual(calls[0].baseRef, undefined, "uncommitted review compares against HEAD");
      assertEqual(result?.baseRef, undefined);
    });
  });

  it("fails closed without calling the observer when none is configured", async () => {
    await withReviewFixture({ observer: false, directory: "/repo" }, async ({ sessionID, calls }) => {
      const result = await runWorkspaceReview(sessionID, "uncommitted");
      assertEqual(calls.length, 0, "no code may leave the machine without an observer");
      assertTrue(result?.ok === false && Boolean(result.error), "missing observer must produce an error verdict");
    });
  });

  it("returns null when the session has no working directory", async () => {
    await withReviewFixture({ observer: true, directory: null }, async ({ sessionID, calls }) => {
      const result = await runWorkspaceReview(sessionID, "uncommitted");
      assertEqual(result, null);
      assertEqual(calls.length, 0);
    });
  });
});
