import { batch, createRoot, createSignal } from "solid-js";
import {
  createPasteCollapse,
  createSessionDraftSync,
  sessionDrafts,
} from "../components/timeline/composer-drafts";
import {
  completeSurfaceOnboarding,
  dismissSurfaceOnboarding,
  setSurfaceMode,
  surfaceMode,
  surfaceOnboardingOpen,
  waitForSurfaceOnboarding,
} from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

describe("first-launch surface onboarding", () => {
  it("holds startup until a surface is chosen, then applies it", async () => {
    const previous = surfaceMode();
    try {
      setSurfaceMode("code");
      let resolved: string | null = null;
      const pending = waitForSurfaceOnboarding().then((mode) => {
        resolved = mode;
        return mode;
      });
      assertEqual(surfaceOnboardingOpen(), true, "chooser opens");
      await Promise.resolve();
      assertEqual(resolved, null, "startup stays on hold before a choice");

      completeSurfaceOnboarding("work");
      assertEqual(await pending, "work");
      assertEqual(surfaceMode(), "work", "chosen surface applied");
      assertEqual(surfaceOnboardingOpen(), true, "chooser stays mounted for its exit animation");

      dismissSurfaceOnboarding();
      assertEqual(surfaceOnboardingOpen(), false, "chooser closes after dismissal");
    } finally {
      dismissSurfaceOnboarding();
      setSurfaceMode(previous);
    }
  });

  it("releases every waiter exactly once", async () => {
    const previous = surfaceMode();
    try {
      const first = waitForSurfaceOnboarding();
      const second = waitForSurfaceOnboarding();
      completeSurfaceOnboarding("code");
      assertEqual(await first, "code");
      assertEqual(await second, "code");

      let late = false;
      void waitForSurfaceOnboarding().then(() => {
        late = true;
      });
      completeSurfaceOnboarding("work");
      await Promise.resolve();
      assertEqual(late, true, "a later wait is served by the next choice");
    } finally {
      dismissSurfaceOnboarding();
      setSurfaceMode(previous);
    }
  });

  it("isolates dual-mounted Code and Work composer draft sync across surface switches", () => {
    const previous = surfaceMode();
    sessionDrafts.clear();
    let disposeRoot: (() => void) | undefined;
    try {
      setSurfaceMode("code");
      const [sid, setSid] = createSignal<string | null>("ses_code_1");
      const [dir, setDir] = createSignal("/code/repo");
      const [codeText, setCodeText] = createSignal("");
      const [workText, setWorkText] = createSignal("");
      let codeRestores = 0;
      let workRestores = 0;

      const makeBridge = () => ({
        images: () => [],
        notice: () => null,
        setNotice: () => {},
        replaceImages: () => {},
        toPromptAttachments: () => [],
      });

      let codeSync!: ReturnType<typeof createSessionDraftSync>;
      let workSync!: ReturnType<typeof createSessionDraftSync>;

      createRoot((dispose) => {
        disposeRoot = dispose;
        codeSync = createSessionDraftSync({
          sessionID: sid,
          directory: dir,
          enabled: () => surfaceMode() !== "work",
          text: codeText,
          setText: setCodeText,
          imgAttach: makeBridge(),
          pasteCollapse: createPasteCollapse(),
          onAfterRestore: () => {
            codeRestores++;
          },
        });
        workSync = createSessionDraftSync({
          sessionID: sid,
          directory: dir,
          enabled: () => surfaceMode() === "work",
          text: workText,
          setText: setWorkText,
          imgAttach: makeBridge(),
          pasteCollapse: createPasteCollapse(),
          onAfterRestore: () => {
            workRestores++;
          },
        });
      });

      setCodeText("unsent code draft");
      assertEqual(workRestores, 0, "hidden Work composer does not restore in Code mode");

      batch(() => {
        setSurfaceMode("work");
        setSid(null);
        setDir("/Users/test/.hoxi/workspaces/default");
      });
      codeSync.sync();
      workSync.sync();

      assertEqual(sessionDrafts.get("ses_code_1")?.text, "unsent code draft", "Code draft preserved when switching to Work");
      assertEqual(workRestores, 1, "Work composer restores once on entering Work mode");
      assertEqual(codeRestores, 0, "hidden Code composer does not run onAfterRestore when leaving Code mode");

      setWorkText("unsent work draft");

      batch(() => {
        setSurfaceMode("code");
        setSid("ses_code_1");
        setDir("/code/repo");
      });
      workSync.sync();
      codeSync.sync();

      assertEqual(codeText(), "unsent code draft", "Code draft restored when returning to Code mode");
      assertEqual(
        sessionDrafts.get("__new__:/Users/test/.hoxi/workspaces/default")?.text,
        "unsent work draft",
        "Work draft preserved when returning to Code mode"
      );
      assertEqual(workRestores, 1, "hidden Work composer does not run onAfterRestore when leaving Work mode");
    } finally {
      disposeRoot?.();
      sessionDrafts.clear();
      setSurfaceMode(previous);
    }
  });
});
