import { type Accessor, createComputed, on, onCleanup, onMount } from "solid-js";
import type { MessageTurn } from "../../store/workspace-state";
import { resolveTurnNavigationTarget } from "./context-activity-turns";

/** Message focus belongs to the originating transcript, including delayed layout retries. */
export function createMessageNavigation(options: {
  sessionID: Accessor<string | null>;
  directory: Accessor<string | undefined>;
  hidden: Accessor<boolean | undefined>;
  turns: Accessor<MessageTurn[]>;
  scroller: Accessor<HTMLElement | undefined>;
  interrupt: () => void;
  pause: () => void;
  ensureTurnVisible: (id: string) => void;
  onNavigate: () => void;
}) {
  let generation = 0;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => {
    generation++;
    if (retryTimer !== undefined) clearTimeout(retryTimer);
    retryTimer = undefined;
  };
  createComputed(on([options.sessionID, options.directory, options.hidden], cancel));
  onCleanup(cancel);

  const navigate = (messageID: string, partID?: string): boolean => {
    const sessionID = options.sessionID();
    if (!sessionID || options.hidden()) return false;
    const turns = options.turns();
    const targetID = resolveTurnNavigationTarget(turns, messageID, partID);
    // A stale event must not pause the new session's live tail even if its target is absent.
    if (!turns.some((turn) => turn.info.id === targetID)) return false;
    const directory = options.directory();
    const revision = generation;
    options.interrupt();
    options.pause();
    options.onNavigate();
    options.ensureTurnVisible(targetID);
    queueMicrotask(() => {
      if (revision !== generation || sessionID !== options.sessionID() || directory !== options.directory()) return;
      const scroller = options.scroller();
      if (!scroller) return;
      const partEl = partID ? scroller.querySelector(`[data-part-id="${CSS.escape(partID)}"]`) : null;
      const element = partEl ?? scroller.querySelector(`[data-turn-id="${CSS.escape(targetID)}"]`);
      if (element instanceof HTMLElement) element.scrollIntoView({ behavior: "smooth", block: "center" });
    });
    return true;
  };

  const scrollToTurn = (messageID: string, partID?: string) => {
    cancel();
    navigate(messageID, partID);
  };

  onMount(() => {
    const focus = (event: Event) => {
      const detail = (event as CustomEvent<{ sessionID?: string; messageID?: string }>).detail;
      if (!detail?.messageID || (detail.sessionID && detail.sessionID !== options.sessionID())) return;
      cancel();
      const revision = generation;
      navigate(detail.messageID);
      retryTimer = setTimeout(() => {
        retryTimer = undefined;
        if (revision === generation) navigate(detail.messageID!);
      }, 450);
    };
    window.addEventListener("hoxi:focus-message", focus);
    onCleanup(() => window.removeEventListener("hoxi:focus-message", focus));
  });

  return { scrollToTurn, cancel };
}
