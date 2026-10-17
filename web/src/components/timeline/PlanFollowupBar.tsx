// web/src/components/timeline/PlanFollowupBar.tsx
import { createMemo, createSignal, Show } from "solid-js";
import { GitBranchPlus, LoaderCircle, Play, Sparkles, X } from "lucide-solid";
import {
  isSessionBusy,
  newIsolatedSession,
  setWorkspace,
  submitSessionPrompt,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

/**
 * Ported from kilocode plan-followup.ts & SuggestBar.tsx:
 * When a turn completes in `plan` mode, surfaces an inline suggestion bar
 * allowing one-click transition to `build` mode or spawning an isolated Worktree.
 */
export default function PlanFollowupBar() {
  const [runningAction, setRunningAction] = createSignal<null | "build" | "worktree">(null);

  const sessionID = () => workspace.activeSessionID;

  // Detect if the latest turn in the active session completed in `plan` mode
  const planTurnInfo = createMemo(() => {
    const id = sessionID();
    if (!id || isSessionBusy(id)) return null;
    const turns = workspace.messagesBySession[id] ?? [];
    if (turns.length === 0) return null;

    const lastTurn = turns[turns.length - 1];
    if (lastTurn.info.role !== "assistant") return null;

    const isPlanAgent =
      (lastTurn.info.agent ?? lastTurn.info.mode ?? "").toLowerCase() === "plan" ||
      workspace.activeMode.toLowerCase() === "plan";
    if (!isPlanAgent) return null;

    const dismissKey = `${id}:${lastTurn.info.id}`;
    if (workspace.dismissedPlanFollowups[dismissKey]) return null;

    const planText = (lastTurn.parts ?? [])
      .filter((p): p is Extract<typeof p, { type: "text" }> => p.type === "text")
      .map((p) => p.text.trim())
      .filter(Boolean)
      .join("\n\n");

    if (planText.length < 40) return null;

    return {
      sessionID: id,
      messageID: lastTurn.info.id,
      dismissKey,
      planText,
    };
  });

  const dismiss = () => {
    const info = planTurnInfo();
    if (!info) return;
    setWorkspace("dismissedPlanFollowups", info.dismissKey, true);
  };

  const implementInCurrentSession = async () => {
    const info = planTurnInfo();
    if (!info || runningAction()) return;
    setRunningAction("build");
    try {
      setWorkspace("dismissedPlanFollowups", info.dismissKey, true);
      setWorkspace("activeMode", "build");
      const prompt = t("timeline.planPromptText");
      await submitSessionPrompt(prompt, info.sessionID, { agent: "build" });
    } finally {
      setRunningAction(null);
    }
  };

  const implementInNewWorktree = async () => {
    const info = planTurnInfo();
    if (!info || runningAction()) return;
    setRunningAction("worktree");
    try {
      setWorkspace("dismissedPlanFollowups", info.dismissKey, true);
      setWorkspace("activeMode", "build");
      const title = t("timeline.implementPlanSessionTitle");
      const newId = await newIsolatedSession(title, { isolated: true });
      const excerpt =
        info.planText.length > 3500
          ? `${info.planText.slice(0, 3500)}\n...(truncated)`
          : info.planText;
      const prompt = t("timeline.worktreePromptTemplate", { excerpt });
      await submitSessionPrompt(prompt, newId, { agent: "build" });
    } finally {
      setRunningAction(null);
    }
  };

  return (
    <Show when={planTurnInfo()}>
      <div class="mb-2 flex items-center justify-between gap-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/55 px-3 py-2 text-xs shadow-xs">
        <div class="flex items-center gap-2 min-w-0">
          <Sparkles size={13} class="text-[var(--accent-primary)] shrink-0" />
          <span class="font-medium text-[var(--text-primary)] truncate">
            {t("timeline.planReadyTitle")}
          </span>
        </div>

        <div class="flex items-center gap-1.5 shrink-0">
          <button
            type="button"
            disabled={!!runningAction()}
            onClick={() => void implementInCurrentSession()}
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Show
              when={runningAction() !== "build"}
              fallback={<LoaderCircle size={11} class="animate-spin" />}
            >
              <Play size={11} class="fill-current" />
            </Show>
            <span>{t("timeline.implementNow")}</span>
          </button>

          <button
            type="button"
            disabled={!!runningAction()}
            onClick={() => void implementInNewWorktree()}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 transition-colors cursor-pointer"
            title={t("timeline.worktreeTooltip")}
          >
            <Show
              when={runningAction() !== "worktree"}
              fallback={<LoaderCircle size={11} class="animate-spin" />}
            >
              <GitBranchPlus size={11} class="text-[var(--accent-primary)]" />
            </Show>
            <span>{t("timeline.newWorktree")}</span>
          </button>

          <button
            type="button"
            onClick={dismiss}
            class="p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] rounded cursor-pointer"
            title={t("timeline.dismiss")}
          >
            <X size={12} />
          </button>
        </div>
      </div>
    </Show>
  );
}
