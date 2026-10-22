import { createEffect, createMemo, createSignal, lazy, onCleanup, onMount, Show, Suspense, untrack, type Accessor } from "solid-js";
import CompactHeader from "./CompactHeader";
import CommandPalette, { type CommandPaletteMode } from "./CommandPalette";
import UpdateModal from "./UpdateModal";
import UpdateDrainNotice from "./UpdateDrainNotice";
import UpdateWatcher from "./UpdateWatcher";
import LeftNav from "./LeftNav";
import MessageStream from "../timeline/MessageStream";
import Composer from "../timeline/Composer";
import ParticleLogo from "../timeline/ParticleLogo";
import ImagePreviewOverlay from "../preview/ImagePreviewOverlay";
import InspectorPane from "../inspector/InspectorPane";
import {
  appSettings,
  closeSettingsModal,
  isUpdateModalOpen,
  leaderboardState,
  LEFT_PANE_MAX,
  LEFT_PANE_MIN,
  leftNavOpen,
  leftWidth,
  openSessionDeepLink,
  openSettingsModal,
  parseSessionHashLink,
  RIGHT_PANE_MAX,
  RIGHT_PANE_MIN,
  rightWidth,
  saveLayoutPrefs,
  selectSession,
  setLeftNavOpen,
  setLeftWidth,
  setRightWidth,
  setWorkspace,
  startDeepLinkNavigation,
  workspace,
} from "../../store/workspace";

const WorkspaceModal = lazy(() => import("./WorkspaceModal"));
const ExternalHandoffModal = lazy(() => import("../handoff/ExternalHandoffModal"));
const ModelLeaderboardModal = lazy(() => import("../models/ModelLeaderboardModal"));
const SettingsModal = lazy(() => import("../settings/SettingsModal"));

const PANE_GUTTERS = 20;
const CENTER_PANE_MIN = 560;

interface ThreePaneShellProps {
  network?: {
    online: Accessor<boolean>;
    connected: Accessor<boolean>;
    streamStarted: Accessor<boolean>;
    onRetry: () => void;
  };
}

export default function ThreePaneShell(props: ThreePaneShellProps = {}) {
  const [windowWidth, setWindowWidth] = createSignal(window.innerWidth);
  const [resizingSide, setResizingSide] = createSignal<"left" | "right" | null>(null);
  const [inspectorEverOpened, setInspectorEverOpened] = createSignal(workspace.inspectorOpen);
  const [paletteMode, setPaletteMode] = createSignal<CommandPaletteMode | null>(null);

  const effectiveLeftWidth = () => (leftNavOpen() ? leftWidth() : 0);
  const rightPaneCeiling = () =>
    Math.max(
      RIGHT_PANE_MIN,
      windowWidth() - PANE_GUTTERS - effectiveLeftWidth() - CENTER_PANE_MIN
    );
  const inspectorWidth = createMemo(() =>
    Math.min(rightWidth(), RIGHT_PANE_MAX, rightPaneCeiling())
  );
  const inspectorFits = () =>
    windowWidth() - PANE_GUTTERS - effectiveLeftWidth() - RIGHT_PANE_MIN >= CENTER_PANE_MIN;

  createEffect(() => {
    if (inspectorFits()) return;
    if (!untrack(() => workspace.inspectorOpen) || workspace.inspectorMaximized) return;
    setWorkspace("inspectorOpen", false);
    setWorkspace("inspectorMaximized", false);
  });

  createEffect(() => {
    if (resizingSide()) return;
    saveLayoutPrefs();
  });

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
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "k" && !ev.shiftKey && !ev.altKey) {
        ev.preventDefault();
        setPaletteMode((current) => (current === "actions" ? null : "actions"));
        return;
      }
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "p" && !ev.shiftKey && !ev.altKey) {
        ev.preventDefault();
        setPaletteMode((current) => (current === "files" ? null : "files"));
        return;
      }
      if ((ev.metaKey || ev.ctrlKey) && ev.key === ",") {
        ev.preventDefault();
        if (appSettings.modalOpen || workspace.providerModalOpen) {
          closeSettingsModal();
        } else {
          openSettingsModal("general");
        }
        return;
      }
      if (ev.key === "Escape" && !ev.defaultPrevented && paletteMode()) {
        ev.preventDefault();
        setPaletteMode(null);
        return;
      }
      if (
        ev.key === "Escape" &&
        !ev.defaultPrevented &&
        (appSettings.modalOpen || workspace.providerModalOpen) &&
        !isUpdateModalOpen() &&
        !leaderboardState.modalOpen &&
        !workspace.handoffModalOpen &&
        workspace.workspaceModalMode === null
      ) {
        ev.preventDefault();
        closeSettingsModal();
      }
    };
    const onNotificationClick = (event: Event) => {
      const sessionID = (event as CustomEvent<{ sessionID?: string }>).detail?.sessionID;
      if (sessionID) void selectSession(sessionID);
    };
    const onWindowResize = () => setWindowWidth(window.innerWidth);
    // Deep-link navigation (pending lifecycle, workspace switching, desktop
    // intents) lives in the store; the shell only feeds it hash changes.
    const focusFromLocationHash = () => openSessionDeepLink(parseSessionHashLink(window.location.hash));
    const stopDeepLinks = startDeepLinkNavigation();
    focusFromLocationHash();
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("hashchange", focusFromLocationHash);
    window.addEventListener("hoxi:notification-click", onNotificationClick);
    window.addEventListener("resize", onWindowResize);
    onCleanup(() => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("hashchange", focusFromLocationHash);
      window.removeEventListener("hoxi:notification-click", onNotificationClick);
      window.removeEventListener("resize", onWindowResize);
      stopDeepLinks();
    });
  });

  const startResize = (side: "left" | "right", ev: PointerEvent) => {
    ev.preventDefault();
    activeResizeCleanup?.();
    setResizingSide(side);
    const startX = ev.clientX;
    const startWidth = side === "left" ? leftWidth() : inspectorWidth();
    let rafId = 0;
    let pendingX = startX;

    const flushResize = () => {
      rafId = 0;
      const delta = pendingX - startX;
      if (side === "left") {
        setLeftWidth(Math.min(LEFT_PANE_MAX, Math.max(LEFT_PANE_MIN, startWidth + delta)));
      } else {
        setRightWidth(
          Math.min(RIGHT_PANE_MAX, rightPaneCeiling(), Math.max(RIGHT_PANE_MIN, startWidth - delta))
        );
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
        network={props.network}
        leftNavOpen={leftNavOpen()}
        onToggleLeftNav={() => setLeftNavOpen((open) => !open)}
        inspectorOpen={workspace.inspectorOpen}
        onOpenCommandPalette={() => setPaletteMode("actions")}
        onToggleInspector={() => {
          const next = !workspace.inspectorOpen;
          setWorkspace("inspectorOpen", next);
          if (!next) {
            setWorkspace("inspectorMaximized", false);
          }
        }}
      />

      <UpdateDrainNotice />

      <main class="flex-1 flex min-h-0 pb-2 pr-2">
        {/* Left Navigation Sidebar (outer clips width transition; inner keeps fixed width for zero text reflow) */}
        <div
          id="workspace-left-nav"
          aria-hidden={!leftNavOpen()}
          class={`shrink-0 h-full overflow-hidden ${
            resizingSide() === "left"
              ? "transition-none"
              : "transition-[width,opacity,transform] duration-[var(--motion-move)] ease-[var(--ease-spring)]"
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
          class={`shrink-0 rounded-full my-2 transition-[width,background-color,opacity] duration-[var(--motion-move)] ${
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
            class={`flex min-h-0 flex-col overflow-hidden transition-[flex-grow,opacity,transform] duration-[var(--motion-move)] ease-[var(--ease-move)] ${
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
                  <ParticleLogo play={isNewConversation()} />
                </div>
              </div>
              <Composer />
            </div>
          </div>
          <div
            aria-hidden="true"
            class="shrink-0 min-h-0 transition-[flex-grow] duration-[var(--motion-move)] ease-[var(--ease-move)] pointer-events-none"
            style={{ "flex-grow": isNewConversation() ? 0.64 : 0 }}
          />
        </section>

        {/* Right Inspector Resize Handle */}
        <div
          class={`shrink-0 rounded-full my-2 transition-[width,background-color,opacity] duration-[var(--motion-move)] ${
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
              : "transition-[width,opacity,transform] duration-[var(--motion-move)] ease-[var(--ease-spring)]"
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
                : { width: `${inspectorWidth()}px` }
          }
        >
          <div
            class="h-full"
            style={{
              width: workspace.inspectorMaximized ? "100%" : `${inspectorWidth()}px`,
            }}
          >
            <Show when={inspectorEverOpened()}>
              <InspectorPane />
            </Show>
          </div>
        </div>
      </main>

      <ImagePreviewOverlay />
      <CommandPalette mode={paletteMode()} onClose={() => setPaletteMode(null)} />
      <UpdateWatcher />
      <UpdateModal />

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
