import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { setWorkspace, workspace } from "../store/workspace";
import { renameSessionTitle, replaceSession } from "../store/workspace-sessions";
import {
  forgetSessionTitles, hasManualSessionTitle, isRedundantSubagentTitle, loadManualSessionTitles,
  preserveSessionTitle, subagentDisplayTitle, summarizeAndRenameSession,
} from "../store/session-title";
import { t } from "../i18n";
import { assertEqual, describe, it } from "./test-harness";

const id = "ses_manual_title_test";

async function withTitleFixture(run: () => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  } });
  setWorkspace("sessions", [{
    id, title: "New Thread", directory: "/title-test", time: { created: 1, updated: 1 },
  }]);
  hoxiAPI.renameSession = async (sessionID, title) => ({
    id: sessionID, title, directory: "/title-test", time: { created: 1, updated: 2 },
  });
  try {
    await run();
  } finally {
    forgetSessionTitles([id]);
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
    if (descriptor) Object.defineProperty(globalThis, "localStorage", descriptor);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
}

const automaticRename = (sessionID: string, title: string) =>
  renameSessionTitle(sessionID, title, "automatic");

describe("Manual session titles", () => {
  it("persists manual titles and rejects automatic writes and upstream titles", async () => {
    await withTitleFixture(async () => {
      let summaries = 0;
      hoxiAPI.summarizeSessionTitle = async () => { summaries++; return { ok: true, title: "AI title" }; };
      await renameSessionTitle(id, "  My task  ");
      assertEqual(loadManualSessionTitles().get(id), "My task");
      await automaticRename(id, "Automatic title");
      await summarizeAndRenameSession(id, "Prompt", undefined, automaticRename);
      assertEqual(summaries, 0);
      replaceSession({ ...workspace.sessions[0], title: "Upstream title", time: { created: 1, updated: 9 } });
      assertEqual(workspace.sessions[0].title, "My task");
      assertEqual(workspace.sessions[0].time.updated, 9);
      assertEqual(preserveSessionTitle({ ...workspace.sessions[0], title: "Loaded title" }).title, "My task");
      await renameSessionTitle(id, "Another manual title");
      assertEqual(workspace.sessions[0].title, "Another manual title");
    });
  });

  it("ignores a generated title arriving after manual rename", async () => {
    await withTitleFixture(async () => {
      let resolve!: (value: { ok: boolean; title: string }) => void;
      hoxiAPI.summarizeSessionTitle = () => new Promise((done) => { resolve = done; });
      // A non-placeholder title starts the summary request without an awaited fallback.
      setWorkspace("sessions", 0, "title", "Initial summary");
      const pending = summarizeAndRenameSession(id, "Prompt", undefined, automaticRename);
      await renameSessionTitle(id, "User title");
      resolve({ ok: true, title: "Late AI title" });
      await pending;
      assertEqual(workspace.sessions[0].title, "User title");
      assertEqual(loadManualSessionTitles().get(id), "User title");
    });
  });

  it("stops generation if manually renamed during the fallback PATCH", async () => {
    await withTitleFixture(async () => {
      const rename = hoxiAPI.renameSession;
      let release!: () => void;
      let summaries = 0;
      hoxiAPI.renameSession = async (sessionID, title, directory) => {
        if (title === "Prompt") await new Promise<void>((done) => { release = done; });
        return rename(sessionID, title, directory);
      };
      hoxiAPI.summarizeSessionTitle = async () => { summaries++; return { ok: true, title: "AI title" }; };
      const pending = summarizeAndRenameSession(id, "Prompt", undefined, automaticRename);
      await renameSessionTitle(id, "User title");
      release();
      await pending;
      assertEqual(summaries, 0);
      assertEqual(workspace.sessions[0].title, "User title");
    });
  });

  it("still generates titles for unlocked sessions without locking them", async () => {
    await withTitleFixture(async () => {
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: true, title: "Generated title" });
      await summarizeAndRenameSession(id, "Prompt", undefined, automaticRename);
      assertEqual(workspace.sessions[0].title, "Generated title");
      assertEqual(hasManualSessionTitle(id), false);
      assertEqual(loadManualSessionTitles().has(id), false);
    });
  });
});

// CJK cases are escaped so check:i18n does not flag them as UI literals.
describe("Subagent titles", () => {
  it("treats restated parent titles as redundant", () => {
    assertEqual(isRedundantSubagentTitle("\u4fee\u590d\u767b\u5f55\u95ee\u9898", "\u4fee\u590d\u767b\u5f55\u95ee\u9898"), true);
    assertEqual(isRedundantSubagentTitle("\u4fee\u590d\u767b\u5f55\u95ee\u9898\u3002", " \u4fee\u590d \u767b\u5f55\u95ee\u9898 (@general subagent)"), true);
    assertEqual(isRedundantSubagentTitle("Fix the login bug", "fix login bug"), false);
    assertEqual(isRedundantSubagentTitle("Fix the login bug", "Fix the login bug quickly"), true);
    assertEqual(isRedundantSubagentTitle("\u6392\u67e5\u5e76\u4fee\u590d\u767b\u5f55\u95ee\u9898", "\u4fee\u590d\u767b\u5f55\u95ee\u9898"), true);
    assertEqual(isRedundantSubagentTitle("\u4fee\u590d\u767b\u5f55\u95ee\u9898", "\u4fee\u590d\u767b\u5f55\u95ee\u9898\u5e76\u8865\u5145\u5168\u90e8\u5355\u5143\u6d4b\u8bd5"), false);
    assertEqual(isRedundantSubagentTitle("\u5206\u6790\u524d\u7aef\u4ee3\u7801\u7ed3\u6784", "\u5206\u6790\u540e\u7aef\u4ee3\u7801\u7ed3\u6784"), false);
    assertEqual(isRedundantSubagentTitle("New Thread", "New Thread"), false);
    assertEqual(isRedundantSubagentTitle("\u4fee", "\u4fee"), true);
    assertEqual(isRedundantSubagentTitle("\u4fee\u590d\u95ee\u9898", "\u4fee"), false);
  });

  it("falls back to an ordinal label and keeps the original for tooltips", () => {
    const repeated = subagentDisplayTitle("Fix login", "Fix login (@explore subagent)", () => 2);
    assertEqual(repeated.redundant, true);
    assertEqual(repeated.label, t("timeline.subagentOrdinalTitle", { index: 2 }));
    assertEqual(repeated.full, "Fix login");
    // "检查自动确认是否有效" → "检查自动确认实现链路" shows "…实现链路".
    const shared = subagentDisplayTitle(
      "\u68c0\u67e5\u81ea\u52a8\u786e\u8ba4\u662f\u5426\u6709\u6548",
      "\u68c0\u67e5\u81ea\u52a8\u786e\u8ba4\u5b9e\u73b0\u94fe\u8def (@explore subagent)",
      () => 1,
    );
    assertEqual(shared.label, "\u2026\u5b9e\u73b0\u94fe\u8def");
    assertEqual(subagentDisplayTitle("Trace Antigravity SSE delay", "Trace Antigravity backend", () => 1).label,
      "\u2026backend");
    // Only a word boundary may cut space-delimited text, and a short shared verb is kept.
    assertEqual(subagentDisplayTitle("Fix login bug", "Fix logout flow", () => 1).label, "Fix logout flow");
    assertEqual(subagentDisplayTitle("Fix the login bug", "Fix the logout flow", () => 1).label,
      "Fix the logout flow");
    const distinct = subagentDisplayTitle("Fix login", "Scan auth middleware", () => 1);
    assertEqual(distinct.label, "Scan auth middleware");
    assertEqual(distinct.redundant, false);
  });
});
