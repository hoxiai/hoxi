import { createSignal, onCleanup, Show } from "solid-js";
import CompactHeader from "./CompactHeader";
import LeftNav from "./LeftNav";
import MessageStream from "../timeline/MessageStream";
import Composer from "../timeline/Composer";
import InspectorPane from "../inspector/InspectorPane";
import ProviderModal from "../models/ProviderModal";
import MultiModelArenaModal from "../models/MultiModelArenaModal";
import { setWorkspace, workspace } from "../../store/workspace";

const LEFT_MIN = 210;
const LEFT_MAX = 340;
const RIGHT_MIN = 320;
const RIGHT_MAX = 640;

export default function ThreePaneShell() {
  const [leftWidth, setLeftWidth] = createSignal(248);
  const [rightWidth, setRightWidth] = createSignal(400);

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
        inspectorOpen={workspace.inspectorOpen}
        onToggleInspector={() => setWorkspace("inspectorOpen", !workspace.inspectorOpen)}
      />

      <main class="flex-1 flex min-h-0 pb-2 pr-2">
        {/* Left Navigation Sidebar */}
        <div class="shrink-0 h-full" style={{ width: `${leftWidth()}px` }}>
          <LeftNav />
        </div>

        {/* Invisible / Subtle Resize Handle */}
        <div
          class="w-1.5 shrink-0 cursor-col-resize rounded-full hover:bg-[var(--accent-border)] transition-colors my-2"
          onPointerDown={(ev) => startResize("left", ev)}
        />

        {/* Center Chat Island (Kilo-style rounded pane-island) */}
        <section class="pane-island relative flex-1 min-w-0 flex flex-col h-full overflow-hidden">
          <MessageStream />
          <Composer />
        </section>

        {/* Right Inspector Island */}
        <Show when={workspace.inspectorOpen}>
          <div
            class="w-1.5 shrink-0 cursor-col-resize rounded-full hover:bg-[var(--accent-border)] transition-colors my-2"
            onPointerDown={(ev) => startResize("right", ev)}
          />
          <div class="pane-island shrink-0 h-full overflow-hidden" style={{ width: `${rightWidth()}px` }}>
            <InspectorPane />
          </div>
        </Show>
      </main>

      {/* Provider & Custom Model Configuration Modal */}
      <ProviderModal />
      <MultiModelArenaModal />
    </div>
  );
}
