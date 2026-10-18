import { createMemo, createSignal, onCleanup, Show } from "solid-js";
import CompactHeader from "./CompactHeader";
import LeftNav from "./LeftNav";
import WorkspaceModal from "./WorkspaceModal";
import ExternalHandoffModal from "../handoff/ExternalHandoffModal";
import MessageStream from "../timeline/MessageStream";
import Composer from "../timeline/Composer";
import ParticleLogo from "../timeline/ParticleLogo";
import InspectorPane from "../inspector/InspectorPane";
import ProviderModal from "../models/ProviderModal";
import ModelLeaderboardModal from "../models/ModelLeaderboardModal";
import SettingsModal from "../settings/SettingsModal";
import { setWorkspace, workspace } from "../../store/workspace";

const LEFT_MIN = 220;
const LEFT_MAX = 360;
const RIGHT_MIN = 340;
const RIGHT_MAX = 640;

export default function ThreePaneShell() {
  const [leftNavOpen, setLeftNavOpen] = createSignal(true);
  const [leftWidth, setLeftWidth] = createSignal(268);
  const [rightWidth, setRightWidth] = createSignal(420);
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

  const startResize = (side: "left" | "right", ev: PointerEvent) => {
    ev.preventDefault();
    const startX = ev.clientX;
    const startWidth = side === "left" ? leftWidth() : rightWidth();

    const onMove = (e: PointerEvent) => {
      const delta = e.clientX - startX;
      if (side === "left") {
        setLeftWidth(Math.min(LEFT_MAX, Math.max(LEFT_MIN, startWidth + delta)));
      } else {
        setRightWidth(Math.min(RIGHT_MAX, Math.max(RIGHT_MIN, startWidth - delta)));
      }
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    onCleanup(onUp);
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

      <main class={`flex-1 flex min-h-0 pb-2 pr-2 ${leftNavOpen() ? "" : "pl-2"}`}>
        {/* Left Navigation Sidebar */}
        <div id="workspace-left-nav" class={`shrink-0 h-full ${leftNavOpen() ? "" : "hidden"}`} style={{ width: `${leftWidth()}px` }}>
          <LeftNav />
        </div>

        {/* Invisible / Subtle Resize Handle */}
        <div
          class={`w-1.5 shrink-0 cursor-col-resize rounded-full hover:bg-[var(--accent-border)] transition-colors my-2 ${leftNavOpen() ? "" : "hidden"}`}
          onPointerDown={(ev) => startResize("left", ev)}
        />

        {/* Center Chat Island (Kilo-style rounded pane-island) */}
        <section
          class={`pane-island relative flex-1 min-w-0 flex flex-col h-full overflow-hidden ${
            workspace.inspectorOpen && workspace.inspectorMaximized ? "hidden" : ""
          }`}
        >
          <MessageStream hidden={isNewConversation()} />
          <div class={`flex flex-col shrink-0 ${isNewConversation() ? "flex-1 min-h-0 overflow-y-auto py-6" : ""}`}>
            <div class={`w-full shrink-0 ${isNewConversation() ? "my-auto flex flex-col [&>div:last-child]:my-0 [&>div:last-child]:pb-1" : ""}`}>
              <Show when={isNewConversation()}>
                <ParticleLogo />
              </Show>
              <Composer />
            </div>
          </div>
        </section>

        {/* Right Inspector Island */}
        <Show when={workspace.inspectorOpen}>
          <Show when={!workspace.inspectorMaximized}>
            <div
              class="w-1.5 shrink-0 cursor-col-resize rounded-full hover:bg-[var(--accent-border)] transition-colors my-2"
              onPointerDown={(ev) => startResize("right", ev)}
            />
          </Show>
          <div
            class={`pane-island h-full overflow-hidden ${
              workspace.inspectorMaximized ? "flex-1 min-w-0" : "shrink-0"
            }`}
            style={
              workspace.inspectorMaximized
                ? undefined
                : { width: `${rightWidth()}px` }
            }
          >
            <InspectorPane />
          </div>
        </Show>
      </main>

      {/* Provider, Custom Model, Workspace, Session Handoff & System Settings Modals */}
      <WorkspaceModal />
      <ExternalHandoffModal />
      <ProviderModal />
      <SettingsModal />
      <ModelLeaderboardModal />
    </div>
  );
}
