import { batch, createEffect, createRenderEffect, createRoot, createSignal } from "solid-js";
import { reconcile, unwrap } from "solid-js/store";
import { isServer } from "solid-js/web";
import { createPresence } from "../components/motion/Presence";
import { createSessionScrollController } from "../components/timeline/session-scroll";
import { createTailHeightHold, createTailHoldState } from "../components/timeline/tail-hold";
import { createTimelineScroll } from "../components/timeline/timeline-scroll";
import { hoxiAPI } from "../services/api";
import {
  dispatchSessionPrompt,
  hydratedSessionIDs,
  isSessionBusy,
  isSessionTaskActive,
  loadSessionMessages,
  selectSession,
  setWorkspace,
  timelineFollowRequest,
  workspace,
  type MessageTurn,
} from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

describe("Session reading restoration", () => {
  it("restores historical anchors, follows latest sessions, and cancels stale switches", () => {
    const originalRAF = globalThis.requestAnimationFrame;
    const originalCancel = globalThis.cancelAnimationFrame;
    let next = 0;
    const frames = new Map<number, FrameRequestCallback>();
    globalThis.requestAnimationFrame = (cb) => { frames.set(++next, cb); return next; };
    globalThis.cancelAnimationFrame = (id) => { frames.delete(id); };
    const flush = () => {
      for (let i = 0; frames.size && i < 10; i++) {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach((cb) => cb(0));
      }
    };
    try {
      let anchorTop = 300;
      let anchorVisible = true;
      const anchor = {
        dataset: { turnId: "old-turn" },
        getBoundingClientRect: (): { top: number; bottom: number } => ({ top: anchorTop - el.scrollTop, bottom: anchorTop + 400 - el.scrollTop }),
      };
      let scrollTop = 0;
      const el = {
        get scrollTop() { return scrollTop; },
        set scrollTop(value: number) { scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)); },
        scrollHeight: 1600,
        clientHeight: 400,
        getBoundingClientRect: () => ({ top: 0 }),
        querySelectorAll: () => anchorVisible ? [anchor] : [],
      };
      const scroll = createTimelineScroll(() => el as unknown as HTMLDivElement, () => true);
      let revealed = "";
      const controller = createSessionScrollController({
        scroller: () => el as unknown as HTMLElement,
        scroll,
        ensureTurnVisible: (id) => { revealed = id; anchorVisible = true; },
      });
      controller.switchSession("A");
      assertEqual(controller.isRestoring(), true);
      controller.ready(["old-turn"], false);
      // Scroll position must be applied synchronously on ready() before any frame paints.
      assertEqual(el.scrollTop, 1200);
      assertEqual(scroll.awayFromBottom(), false);
      flush();
      assertEqual(el.scrollTop, 1200);
      assertEqual(controller.isRestoring(), false);

      el.scrollTop = 350;
      scroll.onScroll();
      controller.capture();
      controller.switchSession("B");
      el.scrollTop = 0;
      controller.ready([], false);
      assertEqual(el.scrollTop, 1200);
      flush();
      assertEqual(el.scrollTop, 1200);

      controller.switchSession("A");
      anchorVisible = false;
      anchorTop = 500; // Layout above the anchor changed while the session was hidden.
      el.scrollTop = 0;
      controller.ready([], true);
      controller.resized();
      flush();
      assertEqual(el.scrollTop, 0);
      controller.ready(["old-turn"], false);
      assertEqual(revealed, "old-turn");
      assertEqual(el.scrollTop, 550);
      flush();
      assertEqual(el.scrollTop, 550);
      assertEqual(scroll.awayFromBottom(), true);
      scroll.schedule(); // Background updates must not restart following.
      flush();
      assertEqual(el.scrollTop, 550);

      controller.switchSession("B");
      controller.ready([], false);
      el.scrollHeight = 2000;
      flush();
      assertEqual(el.scrollTop, 1600);

      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      controller.switchSession("C");
      controller.ready([], false);
      flush();
      assertEqual(el.scrollTop, 1600);

      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      controller.interrupt();
      el.scrollTop = 100;
      flush();
      assertEqual(el.scrollTop, 100);

      // Switching to a bottom-following session and resizing/interrupting
      // during streaming must never pause auto-follow.
      controller.switchSession("C");
      controller.ready([], false);
      flush();
      assertEqual(scroll.isFollowing(), true);
      el.scrollHeight = 2400; // Grows by >64px before scroll ResizeObserver runs
      controller.resized();
      controller.interrupt();
      assertEqual(scroll.isFollowing(), true);

      // Hiding the scroller (clientHeight = 0 while on Launchpad) and WebKit resetting
      // scrollTop to 0 must restore the saved scroll offset immediately when unhidden.
      scroll.pause();
      el.scrollTop = 420;
      controller.capture();
      el.clientHeight = 0;
      el.scrollTop = 0;
      controller.resized();
      el.clientHeight = 400;
      controller.resized();
      assertEqual(el.scrollTop, 420);

      controller.dispose();
      assertEqual(frames.size, 0);
    } finally {
      globalThis.requestAnimationFrame = originalRAF;
      globalThis.cancelAnimationFrame = originalCancel;
    }
  });

  it("jumps directly to the live bottom when switching into or re-selecting an active-task session", () => {
    const originalRAF = globalThis.requestAnimationFrame;
    const originalCancel = globalThis.cancelAnimationFrame;
    let next = 0;
    const frames = new Map<number, FrameRequestCallback>();
    globalThis.requestAnimationFrame = (cb) => { frames.set(++next, cb); return next; };
    globalThis.cancelAnimationFrame = (id) => { frames.delete(id); };
    const flush = () => {
      for (let i = 0; frames.size && i < 10; i++) {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach((cb) => cb(0));
      }
    };
    try {
      const anchor = {
        dataset: { turnId: "old-turn" },
        getBoundingClientRect: (): { top: number; bottom: number } => ({ top: 300 - el.scrollTop, bottom: 700 - el.scrollTop }),
      };
      let scrollTop = 0;
      const el = {
        get scrollTop() { return scrollTop; },
        set scrollTop(value: number) { scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)); },
        scrollHeight: 1600,
        clientHeight: 400,
        getBoundingClientRect: () => ({ top: 0 }),
        querySelectorAll: () => [anchor],
      };
      const busySessions = new Set<string>();
      const scroll = createTimelineScroll(() => el as unknown as HTMLDivElement, () => true);
      const controller = createSessionScrollController({
        scroller: () => el as unknown as HTMLElement,
        scroll,
        ensureTurnVisible: () => {},
        shouldFollowBottom: (id) => busySessions.has(id),
      });

      // User scrolls up in session A and switches to session B.
      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      flush();
      el.scrollTop = 250;
      scroll.onScroll();
      controller.capture();
      assertEqual(scroll.isFollowing(), false);

      controller.switchSession("B");
      controller.ready([], false);
      flush();

      // Session A is now executing a task. Switching back into A must ignore the
      // stale historical scroll offset and jump straight to the active tail.
      busySessions.add("A");
      el.scrollHeight = 2000;
      controller.switchSession("A");
      controller.ready(["old-turn", "live-turn"], false);
      assertEqual(el.scrollTop, 1600);
      assertEqual(scroll.isFollowing(), true);
      assertEqual(scroll.awayFromBottom(), false);
      flush();

      // While staying in busy session A, user scrolls up to inspect history,
      // then clicks session A's title again (`followToBottom`).
      el.scrollTop = 300;
      scroll.onScroll();
      controller.capture();
      assertEqual(scroll.isFollowing(), false);
      controller.followToBottom();
      assertEqual(el.scrollTop, 1600);
      assertEqual(scroll.isFollowing(), true);
      assertEqual(controller.isRestoring(), false);
      flush();

      // Switching into a background-busy session D that has 1 partial SSE turn
      // while full history is still hydrating (`loading === true`) must pin the
      // partial turn immediately and re-pin once full hydration completes.
      busySessions.add("D");
      el.scrollHeight = 600;
      el.scrollTop = 0;
      controller.switchSession("D");
      controller.ready(["partial-sse"], true);
      assertEqual(el.scrollTop, 200);
      assertEqual(controller.isRestoring(), true);
      el.scrollHeight = 2400;
      controller.ready(["h1", "h2", "partial-sse"], false);
      assertEqual(el.scrollTop, 2000);
      flush();
      assertEqual(controller.isRestoring(), false);
      assertEqual(scroll.isFollowing(), true);

      controller.dispose();
    } finally {
      globalThis.requestAnimationFrame = originalRAF;
      globalThis.cancelAnimationFrame = originalCancel;
    }
  });

  it("marks unhydrated background-SSE sessions as loading and emits follow requests on active-task selectSession", async () => {
    const saved = structuredClone(unwrap(workspace));
    const origGetMessages = hoxiAPI.getMessages;
    const origGetPending = hoxiAPI.listPermissions;
    const origGetDiffs = hoxiAPI.getSessionDiff;
    try {
      let resolveMessages!: (turns: MessageTurn[]) => void;
      hoxiAPI.getMessages = () => new Promise((r) => { resolveMessages = r; });
      hoxiAPI.listPermissions = async () => [];
      hoxiAPI.getSessionDiff = async () => [];

      hydratedSessionIDs.delete("busy-sse");
      hydratedSessionIDs.add("idle-hydrated");
      setWorkspace(reconcile({
        ...saved,
        rootDir: "/proj",
        activeSessionID: "idle-hydrated",
        sessions: [
          { id: "idle-hydrated", title: "Idle", directory: "/proj", time: { created: 1, updated: 1 } },
          { id: "busy-sse", title: "Busy", directory: "/proj", time: { created: 2, updated: 2 } },
        ],
        statusBySession: { "busy-sse": { type: "busy" } },
        messagesBySession: {
          "idle-hydrated": [],
          // Partial turn already pushed by background SSE before first open:
          "busy-sse": [{
            info: { id: "m-live", sessionID: "busy-sse", role: "assistant", time: { created: 2 } },
            parts: [],
          }],
        },
        loadingMessagesBySession: {},
        pendingPermissions: [],
        pendingQuestions: [],
      }));

      assertEqual(isSessionTaskActive("idle-hydrated"), false);
      assertEqual(isSessionTaskActive("busy-sse"), true);

      const prevSeq = timelineFollowRequest()?.seq ?? 0;
      const promise = selectSession("busy-sse");
      // Even though messagesBySession["busy-sse"] had a partial SSE turn, it is
      // not hydrated yet so loadingMessagesBySession must stay true until getMessages resolves.
      assertEqual(workspace.activeSessionID, "busy-sse");
      assertEqual(workspace.loadingMessagesBySession["busy-sse"], true);
      assertEqual(timelineFollowRequest()?.sessionID, "busy-sse");
      assertEqual((timelineFollowRequest()?.seq ?? 0) > prevSeq, true);

      resolveMessages([
        { info: { id: "m-old", sessionID: "busy-sse", role: "user", time: { created: 1 } }, parts: [] },
        { info: { id: "m-live", sessionID: "busy-sse", role: "assistant", time: { created: 2 } }, parts: [] },
      ]);
      await promise;
      assertEqual(workspace.loadingMessagesBySession["busy-sse"], false);
      assertEqual(hydratedSessionIDs.has("busy-sse"), true);

      // Re-clicking the already-active session title while busy or awaiting approval
      // bumps timelineFollowRequest again so scrolled-up readers snap back to the focus point.
      const seqAfterHydrate = timelineFollowRequest()?.seq ?? 0;
      setWorkspace("aiBySession", "busy-sse", { agent: "build", model: { providerID: "p", modelID: "m" }, effort: "" });
      await selectSession("busy-sse");
      assertEqual((timelineFollowRequest()?.seq ?? 0) > seqAfterHydrate, true);
    } finally {
      hydratedSessionIDs.delete("busy-sse");
      hydratedSessionIDs.delete("idle-hydrated");
      setWorkspace(reconcile(saved));
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.listPermissions = origGetPending;
      hoxiAPI.getSessionDiff = origGetDiffs;
    }
  });

  it("unmounts 0ms Presence bodies in the render phase and reconciles tailHold spacer before effects finish", () => {
    const state = createTailHoldState();
    assertEqual(state.measure(1000, 58, true), 0);
    assertEqual(state.handOff(), 58);
    // When the new reply (+70px) replaces the waiting row (-58px) for a natural height of 1012px,
    // immediate post-render measurement clamps the 58px hand-off spacer down to 0px before paint.
    assertEqual(state.measure(1012 + 58, 0, false), 0);

    // When the new reply (+40px) is shorter than the waiting row (-58px) for a natural height of 982px,
    // immediate post-render measurement clamps the spacer to the exact 18px difference.
    assertEqual(state.measure(1000, 58, true), 0);
    assertEqual(state.handOff(), 58);
    assertEqual(state.measure(982 + 58, 0, false), 18);

    // Node's SSR runtime intentionally does not rerun reactive computations after mount.
    if (isServer) return;

    let dispose = () => {};
    try {
      const [open, setOpen] = createSignal(true);
      const [exitMs, setExitMs] = createSignal(180);
      const [sessionID] = createSignal<string | null>("ses_tail_hold");
      const [waiting, setWaiting] = createSignal(true);
      const [busy] = createSignal(true);
      let mountedWhenEffectRan = true;
      let naturalHeight = 1000;
      let rowHeight = 58;

      const { presence, hold, listEl } = createRoot((d) => {
        dispose = d;
        const p = createPresence(open, exitMs);
        createEffect(() => {
          if (!open()) {
            mountedWhenEffectRan = p.mounted();
          }
        });

        const h = createTailHeightHold({ sessionID, waiting, busy });
        const list = {
          get offsetHeight() {
            return naturalHeight + h.spacer();
          },
        } as unknown as HTMLElement;
        const row = {
          get offsetHeight() {
            return rowHeight;
          },
        } as unknown as HTMLElement;
        h.list(list);
        h.row(row);
        createRenderEffect(() => {
          if (!waiting()) {
            rowHeight = 0;
            naturalHeight = 1012;
          }
        });
        return { presence: p, hold: h, listEl: list };
      });

      batch(() => {
        setExitMs(0);
        setOpen(false);
      });
      assertEqual(presence.mounted(), false);
      assertEqual(mountedWhenEffectRan, false);

      setWaiting(false);
      assertEqual(hold.spacer(), 0);
      assertEqual(listEl.offsetHeight, 1012);
    } finally {
      dispose();
    }
  });

  it("stages follow-up turns synchronously and invalidates stale pre-send message loads", async () => {
    const saved = structuredClone(unwrap(workspace));
    const origGetMessages = hoxiAPI.getMessages;
    const origSendPrompt = hoxiAPI.sendPromptAsync;
    const origGetDiffs = hoxiAPI.getSessionDiff;
    const origSetTimeout = globalThis.setTimeout;
    globalThis.setTimeout = (() => 0) as unknown as typeof setTimeout;
    try {
      let resolveStaleLoad!: (turns: MessageTurn[]) => void;
      hoxiAPI.getMessages = () => new Promise((r) => { resolveStaleLoad = r; });
      hoxiAPI.sendPromptAsync = async () => {};
      hoxiAPI.getSessionDiff = async () => [];

      const sid = "ses_followup_sync";
      hydratedSessionIDs.add(sid);
      setWorkspace("rootDir", "/proj");
      setWorkspace("activeSessionID", sid);
      setWorkspace("connected", true);
      setWorkspace("providers", [{ id: "p", name: "P", models: { m: { id: "m" } } }]);
      setWorkspace("sessions", [{ id: sid, title: "Followup", directory: "/proj", time: { created: 1, updated: 1 } }]);
      setWorkspace("aiBySession", sid, { agent: "build", model: { providerID: "p", modelID: "m" }, effort: "" });
      setWorkspace("statusBySession", sid, { type: "idle" });
      setWorkspace("messagesBySession", sid, [
        { info: { id: "m_1", sessionID: sid, role: "user", time: { created: 1 } }, parts: [] },
      ]);

      // Start an idle loadSessionMessages before sending the 2nd message.
      const staleLoadPromise = loadSessionMessages(sid);
      const sendPromise = dispatchSessionPrompt("Second message", sid);

      // Optimistic turn and busy status must be staged synchronously before sendPromise awaits HTTP.
      assertEqual(isSessionBusy(sid), true);
      assertEqual(workspace.messagesBySession[sid]?.length, 2);

      // Resolving the stale pre-send getMessages snapshot must not overwrite or prune the staged turn.
      resolveStaleLoad([{ info: { id: "m_1", sessionID: sid, role: "user", time: { created: 1 } }, parts: [] }]);
      await staleLoadPromise;
      await sendPromise;
      assertEqual(workspace.messagesBySession[sid]?.length, 2);
    } finally {
      globalThis.setTimeout = origSetTimeout;
      hydratedSessionIDs.delete("ses_followup_sync");
      setWorkspace("activeSessionID", saved.activeSessionID);
      setWorkspace("aiBySession", reconcile(saved.aiBySession));
      setWorkspace(reconcile(saved));
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.sendPromptAsync = origSendPrompt;
      hoxiAPI.getSessionDiff = origGetDiffs;
    }
  });
});
