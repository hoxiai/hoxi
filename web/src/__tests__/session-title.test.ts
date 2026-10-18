import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { setWorkspace, workspace } from "../store/workspace";
import { renameSessionTitle, replaceSession } from "../store/workspace-sessions";
import {
  forgetSessionTitles, hasManualSessionTitle, preserveSessionTitle, summarizeAndRenameSession,
} from "../store/session-title";
import { loadManualSessionTitles } from "../store/session-title-storage";
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
