import { createEffect, createMemo, createSignal, lazy, onCleanup, onMount, Show, Suspense } from "solid-js";
import CompactHeader from "./CompactHeader";
import LeftNav from "./LeftNav";
import MessageStream from "../timeline/MessageStream";
import Composer from "../timeline/Composer";
import ParticleLogo from "../timeline/ParticleLogo";
import InspectorPane from "../inspector/InspectorPane";
import {
  appSettings,
  closeSettingsModal,
  leaderboardState,
  openSettingsModal,
  setWorkspace,
  workspace,
} from "../../store/workspace";

const WorkspaceModal = lazy(() => import("./WorkspaceModal"));
const ExternalHandoffModal = lazy(() => import("../handoff/ExternalHandoffModal"));
const ModelLeaderboardModal = lazy(() => import("../models/ModelLeaderboardModal"));
const SettingsModal = lazy(() => import("../settings/SettingsModal"));

const LEFT_MIN = 220;
const LEFT_MAX = 360;
const RIGHT_MIN = 340;
const RIGHT_MAX = 640;

export default function ThreePaneShell() {
  const [leftNavOpen, setLeftNavOpen] = createSignal(true);
  const [leftWidth, setLeftWidth] = createSignal(268);
  const [rightWidth, setRightWidth] = createSignal(420);
  const [resizingSide, setResizingSide] = createSignal<"left" | "right" | null>(null);
  const [inspectorEverOpened, setInspectorEverOpened] = createSignal(workspace.inspectorOpen);

  createEffect(() => {
    if (workspace.inspectorOpen) {
      setInspectorEverOpened(true);
    }
  });

  const isNewConversation = createMemo(() => {
    if (workspace.bootstrappingSessions) return false;
    const id = workspace.activeSessionID;
    if (!id) return true;
    const status = workspace.statusBySession[id];
    return !workspace.loadingMessagesBySession[id]
      && (workspace.messagesBySession[id]?.length ?? 0) === 0
      && (!status || status.type === "idle")
      && !workspace.sessions.find((session) => session.id === id)?.revert
      && !workspace.pendingPermissions.some((permission) => permission.sessionID === id)
      && !workspace.pendingQuestions.some((question) => question.sessionID === id);
  });

  let activeResizeCleanup: (() => void) | null = null;
  onCleanup(() => activeResizeCleanup?.());

  onMount(() => {
    const onKeyDown = (ev: KeyboardEvent) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key === ",") {
        ev.preventDefault();
        if (appSettings.modalOpen || workspace.providerModalOpen) {
          closeSettingsModal();
        } else {
          openSettingsModal("general");
        }
        return;
      }
      if (
        ev.key === "Escape" &&
        !ev.defaultPrevented &&
        (appSettings.modalOpen || workspace.providerModalOpen) &&
        !leaderboardState.modalOpen &&
        !workspace.handoffModalOpen &&
        workspace.workspaceModalMode === null
      ) {
        ev.preventDefault();
        closeSettingsModal();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

  const startResize = (side: "left" | "right", ev: PointerEvent) => {
    ev.preventDefault();
    activeResizeCleanup?.();
    setResizingSide(side);
    const startX = ev.clientX;
    const startWidth = side === "left" ? leftWidth() : rightWidth();
    let rafId = 0;
    let pendingX = startX;

    const flushResize = () => {
      rafId = 0;
      const delta = pendingX - startX;
      if (side === "left") {
        setLeftWidth(Math.min(LEFT_MAX, Math.max(LEFT_MIN, startWidth + delta)));
      } else {
        setRightWidth(Math.min(RIGHT_MAX, Math.max(RIGHT_MIN, startWidth - delta)));
      }
    };

    const onMove = (e: PointerEvent) => {
      pendingX = e.clientX;
      if (!rafId) {
        rafId = requestAnimationFrame(flushResize);
      }
    };
    const onUp = () => {
      if (rafId) {
        cancelAnimationFrame(rafId);
        rafId = 0;
      }
      setResizingSide(null);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      if (activeResizeCleanup === onUp) {
        activeResizeCleanup = null;
      }
    };
    activeResizeCleanup = onUp;
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
  };

  return (
    <div class="h-screen w-screen flex flex-col bg-[var(--bg-canvas)] text-[var(--text-primary)] overflow-hidden">
      <CompactHeader
        leftNavOpen={leftNavOpen()}
        onToggleLeftNav={() => setLeftNavOpen((open) => !open)}
        inspectorOpen={workspace.inspectorOpen}
        onToggleInspector={() => {
          const next = !workspace.inspectorOpen;
          setWorkspace("inspectorOpen", next);
          if (!next) {
            setWorkspace("inspectorMaximized", false);
          }
        }}
      />

      <main class="flex-1 flex min-h-0 pb-2 pr-2">
        {/* Left Navigation Sidebar (outer clips width transition; inner keeps fixed width for zero text reflow) */}
        <div
          id="workspace-left-nav"
          aria-hidden={!leftNavOpen()}
          class={`shrink-0 h-full overflow-hidden ${
            resizingSide() === "left"
              ? "transition-none"
              : "transition-[width,opacity,transform] duration-200 ease-[var(--ease-spring)]"
          } ${
            leftNavOpen()
              ? "opacity-100 translate-x-0"
              : "opacity-0 -translate-x-2 pointer-events-none"
          }`}
          style={{ width: leftNavOpen() ? `${leftWidth()}px` : "0px" }}
        >
          <div class="h-full" style={{ width: `${leftWidth()}px` }}>
            <LeftNav />
          </div>
        </div>

        {/* Left Resize Handle / Collapsed Left Spacer */}
        <div
          class={`shrink-0 rounded-full my-2 transition-[width,background-color,opacity] duration-200 ${
            leftNavOpen()
              ? "w-1.5 cursor-col-resize hover:bg-[var(--accent-border)] opacity-100"
              : "w-2 pointer-events-none opacity-0"
          }`}
          onPointerDown={(ev) => leftNavOpen() && startResize("left", ev)}
        />

        {/* Center Chat Island (Kilo-style rounded pane-island) */}
        <section
          class={`pane-island relative flex-1 min-w-0 flex flex-col h-full overflow-hidden ${
            workspace.inspectorOpen && workspace.inspectorMaximized ? "hidden" : ""
          }`}
        >
          <div
            aria-hidden={isNewConversation()}
            class={`flex min-h-0 flex-col overflow-hidden transition-[flex-grow,opacity,transform] duration-250 ease-[var(--ease-spring)] ${
              isNewConversation()
                ? "flex-[0.36_1_0%] opacity-0 -translate-y-1 pointer-events-none"
                : "flex-[1_1_0%] opacity-100 translate-y-0"
            }`}
          >
            <MessageStream hidden={false} />
          </div>
          <div class="flex flex-col shrink-0 pt-2">
            <div class="w-full shrink-0 flex flex-col [&>div:last-child]:my-0">
              <div
                class="hoxi-drawer-grid"
                data-open={isNewConversation() ? "true" : "false"}
                aria-hidden={!isNewConversation()}
              >
                <div class="min-h-0 overflow-hidden">
                  <ParticleLogo />
                </div>
              </div>
              <Composer />
            </div>
          </div>
          <div
            aria-hidden="true"
            class="shrink-0 min-h-0 transition-[flex-grow] duration-250 ease-[var(--ease-spring)] pointer-events-none"
            style={{ "flex-grow": isNewConversation() ? 0.64 : 0 }}
          />
        </section>

        {/* Right Inspector Resize Handle */}
        <div
          class={`shrink-0 rounded-full my-2 transition-[width,background-color,opacity] duration-200 ${
            workspace.inspectorOpen && !workspace.inspectorMaximized
              ? "w-1.5 cursor-col-resize hover:bg-[var(--accent-border)] opacity-100"
              : "w-0 pointer-events-none opacity-0"
          }`}
          onPointerDown={(ev) =>
            workspace.inspectorOpen &&
            !workspace.inspectorMaximized &&
            startResize("right", ev)
          }
        />

        {/* Right Inspector Island (smooth width + opacity transition, inner fixed width prevents child layout thrash) */}
        <div
          aria-hidden={!workspace.inspectorOpen}
          class={`pane-island h-full overflow-hidden ${
            resizingSide() === "right"
              ? "transition-none"
              : "transition-[width,opacity,transform] duration-200 ease-[var(--ease-spring)]"
          } ${
            !workspace.inspectorOpen
              ? "shrink-0 opacity-0 translate-x-2 pointer-events-none !border-transparent !shadow-none"
              : workspace.inspectorMaximized
                ? "flex-1 min-w-0 opacity-100 translate-x-0"
                : "shrink-0 opacity-100 translate-x-0"
          }`}
          style={
            !workspace.inspectorOpen
              ? { width: "0px" }
              : workspace.inspectorMaximized
                ? undefined
                : { width: `${rightWidth()}px` }
          }
        >
          <div
            class="h-full"
            style={{
              width: workspace.inspectorMaximized ? "100%" : `${rightWidth()}px`,
            }}
          >
            <Show when={inspectorEverOpened()}>
              <InspectorPane />
            </Show>
          </div>
        </div>
      </main>

      {/* Workspace, Session Handoff, Leaderboard Modals & Full-Screen Settings Control Center */}
      <Suspense>
        <Show when={workspace.workspaceModalMode !== null}>
          <WorkspaceModal />
        </Show>
        <Show when={workspace.handoffModalOpen}>
          <ExternalHandoffModal />
        </Show>
        <Show when={leaderboardState.modalOpen}>
          <ModelLeaderboardModal />
        </Show>
        <Show when={appSettings.modalOpen || workspace.providerModalOpen}>
          <SettingsModal />
        </Show>
      </Suspense>
    </div>
  );
}
