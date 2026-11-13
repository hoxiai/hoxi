import { createEffect, createMemo, createSignal, lazy, onCleanup, onMount, Show, Suspense, untrack, type Accessor } from "solid-js";
import CompactHeader from "./CompactHeader";
import type { CommandPaletteMode } from "./CommandPalette";
import UpdateDrainNotice from "./UpdateDrainNotice";
import UpdateWatcher from "./UpdateWatcher";
import LeftNav from "./LeftNav";
import MessageStream from "../timeline/MessageStream";
import Composer from "../timeline/Composer";
import ParticleLogo from "../timeline/ParticleLogo";
import {
  activateSurfaceMode,
  appSettings,
  canFitRightPane,
  clampRightPaneWidth,
  closeSettingsModal,
  DEFAULT_LEFT_WIDTH,
  DEFAULT_RIGHT_WIDTH,
  imageViewerOpen,
  isWorkSurfacePath,
  isUpdateModalOpen,
  leaderboardState,
  LEFT_PANE_MAX,
  LEFT_PANE_MIN,
  leftNavOpen,
  leftWidth,
  openSessionDeepLink,
  openSettingsModal,
  parseSessionHashLink,
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
import { isWorkSurface, surfaceExtension } from "./surface-extension";

const InspectorPane = lazy(() => import("../inspector/InspectorPane"));
const CommandPalette = lazy(() => import("./CommandPalette"));
const UpdateModal = lazy(() => import("./UpdateModal"));
const ImagePreviewOverlay = lazy(() => import("../preview/ImagePreviewOverlay"));
const WorkspaceModal = lazy(() => import("./WorkspaceModal"));
const ExternalHandoffModal = lazy(() => import("../handoff/ExternalHandoffModal"));
const ModelLeaderboardModal = lazy(() => import("../models/ModelLeaderboardModal"));
const SettingsModal = lazy(() => import("../settings/SettingsModal"));

interface ThreePaneShellProps {
  network?: {
    online: Accessor<boolean>;
    connected: Accessor<boolean>;
    streamStarted: Accessor<boolean>;
    onRetry: () => void;
  };
}

function useEverOpened(when: () => boolean): Accessor<boolean> {
  return createMemo<boolean>((prev) => prev || Boolean(when()), false);
}

export default function ThreePaneShell(props: ThreePaneShellProps = {}) {
  const [windowWidth, setWindowWidth] = createSignal(window.innerWidth);
  const [resizingSide, setResizingSide] = createSignal<"left" | "right" | null>(null);
  const [paletteMode, setPaletteMode] = createSignal<CommandPaletteMode | null>(null);
  const inspectorEverOpened = useEverOpened(() => workspace.inspectorOpen);
  const imageViewerEverOpened = useEverOpened(imageViewerOpen);
  const paletteEverOpened = useEverOpened(() => paletteMode() !== null);
  const updateModalEverOpened = useEverOpened(isUpdateModalOpen);
  const workspaceModalEverOpened = useEverOpened(() => workspace.workspaceModalMode !== null);
  const handoffModalEverOpened = useEverOpened(() => workspace.handoffModalOpen);
  const leaderboardEverOpened = useEverOpened(() => leaderboardState.modalOpen);
  const settingsEverOpened = useEverOpened(() => appSettings.modalOpen);

  const effectiveLeftWidth = () => (leftNavOpen() ? leftWidth() : 0);
  const inspectorWidth = createMemo(() =>
    clampRightPaneWidth(rightWidth(), windowWidth(), effectiveLeftWidth())
  );
  const inspectorFits = () => canFitRightPane(windowWidth(), effectiveLeftWidth());

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

  const isNewConversation = createMemo(() => {
    const rootIsWork = isWorkSurfacePath(workspace.rootDir);
    if (isWorkSurface() && !rootIsWork) return true;
    if (!isWorkSurface() && rootIsWork) return true;
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
      if (surfaceExtension.enabled && (ev.metaKey || ev.ctrlKey) && !ev.shiftKey && !ev.altKey) {
        if (ev.key === "1") {
          ev.preventDefault();
          void activateSurfaceMode("work");
          return;
        }
        if (ev.key === "2") {
          ev.preventDefault();
          void activateSurfaceMode("code");
          return;
        }
      }
      if ((ev.metaKey || ev.ctrlKey) && ev.key === ",") {
        ev.preventDefault();
        if (appSettings.modalOpen) {
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
        appSettings.modalOpen &&
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
    onWindowResize();
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
    if (ev.button !== 0) return;
    ev.preventDefault();
    activeResizeCleanup?.();
    setWindowWidth(window.innerWidth);
    setResizingSide(side);
    const handle = ev.currentTarget as HTMLElement | null;
    const pointerId = ev.pointerId;
    if (handle?.setPointerCapture) {
      try {
        handle.setPointerCapture(pointerId);
      } catch {
        /* synthetic or already-released pointer */
      }
    }
    const prevBodyCursor = document.body.style.cursor;
    document.body.style.cursor = "col-resize";
    const startX = ev.clientX;
    const startWidth = side === "left" ? leftWidth() : inspectorWidth();
    let rafId = 0;
    let pendingX = startX;

    const applyDelta = (clientX: number) => {
      const delta = clientX - startX;
      if (side === "left") {
        setLeftWidth(Math.min(LEFT_PANE_MAX, Math.max(LEFT_PANE_MIN, Math.round(startWidth + delta))));
      } else {
        setRightWidth(
          clampRightPaneWidth(Math.round(startWidth - delta), windowWidth(), effectiveLeftWidth())
        );
      }
    };

    const flushResize = () => {
      rafId = 0;
      applyDelta(pendingX);
    };

    const onMove = (e: PointerEvent) => {
      pendingX = e.clientX;
      if (!rafId) {
        rafId = requestAnimationFrame(flushResize);
      }
    };
    const onUp = (e?: PointerEvent) => {
      if (rafId) {
        cancelAnimationFrame(rafId);
        rafId = 0;
        applyDelta(e && Number.isFinite(e.clientX) ? e.clientX : pendingX);
      }
      if (handle?.hasPointerCapture?.(pointerId)) {
        try {
          handle.releasePointerCapture(pointerId);
        } catch {
          /* ignore */
        }
      }
      document.body.style.cursor = prevBodyCursor;
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
    <div
      data-surface={isWorkSurface() ? "work" : "code"}
      class="h-screen w-screen flex flex-col bg-[var(--bg-canvas)] text-[var(--text-primary)] overflow-hidden"
    >
      <Show when={surfaceExtension.enabled && surfaceExtension.renderTheme}>
        {surfaceExtension.renderTheme!()}
      </Show>
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
          role="separator"
          aria-orientation="vertical"
          style={{ "--wails-draggable": "no-drag" } as any}
          class={`group/resize relative z-20 shrink-0 my-2 select-none touch-none transition-[width,opacity] duration-[var(--motion-move)] ${
            leftNavOpen()
              ? "w-2 cursor-col-resize opacity-100"
              : "w-2 pointer-events-none opacity-0"
          }`}
          onDblClick={() => leftNavOpen() && setLeftWidth(DEFAULT_LEFT_WIDTH)}
          onPointerDown={(ev) => leftNavOpen() && startResize("left", ev)}
        >
          <div class="absolute inset-y-0 -left-0.5 -right-1.5" />
          <div
            class={`mx-auto h-full w-1 rounded-full transition-colors ${
              resizingSide() === "left"
                ? "bg-[var(--accent-primary)]"
                : "bg-transparent group-hover/resize:bg-[var(--accent-border)]"
            }`}
          />
        </div>

        {/* Center Chat Island (Kilo-style rounded pane-island) */}
        <section
          class={`pane-island relative flex-1 min-w-0 flex flex-col h-full overflow-hidden ${
            workspace.inspectorOpen && workspace.inspectorMaximized ? "hidden" : ""
          } ${resizingSide() ? "pointer-events-none select-none" : ""}`}
        >
          <Show when={!(isWorkSurface() && surfaceExtension.isMainViewActive?.() && surfaceExtension.renderMainView)} fallback={surfaceExtension.renderMainView?.()}>
          <div
            aria-hidden={isNewConversation()}
            class={`flex min-h-0 flex-col overflow-hidden ${
              isNewConversation()
                ? "flex-[0.36_1_0%] pointer-events-none"
                : "flex-[1_1_0%]"
            }`}
          >
            <div class={isNewConversation() ? "hidden" : "flex flex-1 min-h-0 flex-col overflow-hidden"}>
              <MessageStream hidden={isNewConversation()} />
            </div>
          </div>
          <div class="flex flex-col shrink-0 pt-2">
            <div class="w-full shrink-0 flex flex-col [&>div:last-child]:my-0">
              <div
                aria-hidden={!isNewConversation()}
                class={isNewConversation() ? "block" : "hidden"}
              >
                <div class="min-h-0 overflow-hidden">
                  <Show when={surfaceExtension.enabled && surfaceExtension.renderLaunchpad}>
                    <div class={isWorkSurface() ? "block" : "hidden"}>
                      {surfaceExtension.renderLaunchpad!()}
                    </div>
                  </Show>
                  <div class={isWorkSurface() && surfaceExtension.renderLaunchpad ? "hidden" : "block"}>
                    <ParticleLogo play={isNewConversation() && !isWorkSurface()} />
                  </div>
                </div>
              </div>
              <Show when={surfaceExtension.enabled && surfaceExtension.renderComposer}>
                <div class={isWorkSurface() ? "block" : "hidden"}>
                  {surfaceExtension.renderComposer!()}
                </div>
              </Show>
              <div class={isWorkSurface() && surfaceExtension.renderComposer ? "hidden" : "block"}>
                <Composer />
              </div>
            </div>
          </div>
          <div
            aria-hidden="true"
            class="shrink-0 min-h-0 pointer-events-none"
            style={{
              "flex-grow": isNewConversation() ? 0.64 : 0,
            }}
          />
          </Show>
        </section>

        {/* Right Inspector Resize Handle */}
        <div
          role="separator"
          aria-orientation="vertical"
          style={{ "--wails-draggable": "no-drag" } as any}
          class={`group/resize relative z-20 shrink-0 my-2 select-none touch-none transition-[width,opacity] duration-[var(--motion-move)] ${
            workspace.inspectorOpen && !workspace.inspectorMaximized
              ? "w-2 cursor-col-resize opacity-100"
              : "w-0 pointer-events-none opacity-0"
          }`}
          onDblClick={() =>
            workspace.inspectorOpen &&
            !workspace.inspectorMaximized &&
            setRightWidth(DEFAULT_RIGHT_WIDTH)
          }
          onPointerDown={(ev) =>
            workspace.inspectorOpen &&
            !workspace.inspectorMaximized &&
            startResize("right", ev)
          }
        >
          <div class="absolute inset-y-0 -left-0.5 -right-1.5" />
          <div
            class={`mx-auto h-full w-1 rounded-full transition-colors ${
              resizingSide() === "right"
                ? "bg-[var(--accent-primary)]"
                : "bg-transparent group-hover/resize:bg-[var(--accent-border)]"
            }`}
          />
        </div>

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
          } ${resizingSide() ? "pointer-events-none select-none" : ""}`}
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
            <Suspense>
              <Show when={inspectorEverOpened()}>
                <InspectorPane />
              </Show>
            </Suspense>
          </div>
        </div>
      </main>

      <UpdateWatcher />

      {/* On-demand overlays, Workspace, Session Handoff, Leaderboard Modals & Full-Screen Settings Control Center */}
      <Suspense>
        <Show when={imageViewerEverOpened()}>
          <ImagePreviewOverlay />
        </Show>
        <Show when={paletteEverOpened()}>
          <CommandPalette mode={paletteMode()} onClose={() => setPaletteMode(null)} />
        </Show>
        <Show when={updateModalEverOpened()}>
          <UpdateModal />
        </Show>
        <Show when={workspaceModalEverOpened()}>
          <WorkspaceModal />
        </Show>
        <Show when={handoffModalEverOpened()}>
          <ExternalHandoffModal />
        </Show>
        <Show when={leaderboardEverOpened()}>
          <ModelLeaderboardModal />
        </Show>
        <Show when={settingsEverOpened()}>
          <SettingsModal />
        </Show>
      </Suspense>
    </div>
  );
}
