// web/src/components/timeline/RevertBanner.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { LoaderCircle, RotateCcw, Undo2 } from "lucide-solid";
import {
  openDiffInInspector,
  revertToHere,
  undoRevert,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";

/**
 * Ported from kilocode RevertBanner.tsx:
 * Shows when the active session is in a reverted checkpoint state,
 * displaying the reverted turn count, per-file diff stats, Step Redo, and Restore All actions.
 */
export default function RevertBanner() {
  const [busy, setBusy] = createSignal(false);

  const activeSession = createMemo(() => {
    const id = workspace.activeSessionID;
    if (!id) return null;
    return workspace.sessions.find((s) => s.id === id) ?? null;
  });

  const revertBoundary = createMemo(() => activeSession()?.revert?.messageID ?? null);

  const sessionDiffs = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.diffsBySession[id] ?? [] : [];
  });

  const onStepRedo = async () => {
    const id = workspace.activeSessionID;
    const boundary = revertBoundary();
    if (!id || !boundary || busy()) return;

    setBusy(true);
    try {
      const turns = workspace.messagesBySession[id] ?? [];
      const nextUser = turns.find(
        (turn) => turn.info.role === "user" && turn.info.id > boundary
      );
      if (nextUser) {
        await revertToHere(id, nextUser.info.id);
      } else {
        await undoRevert(id);
      }
    } catch (err) {
      console.error("step redo failed:", err);
    } finally {
      setBusy(false);
    }
  };

  const onRestoreAll = async () => {
    const id = workspace.activeSessionID;
    if (!id || busy()) return;
    setBusy(true);
    try {
      await undoRevert(id);
    } catch (err) {
      console.error("restore all failed:", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Show when={revertBoundary()}>
      <div class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)]/90 px-3.5 py-2.5 shadow-xs space-y-2">
        <div class="flex items-center justify-between gap-2">
          <div class="flex items-center gap-2 min-w-0">
            <Undo2 size={14} class="text-[var(--warn-text)] shrink-0" />
            <span class="truncate text-[12px] font-medium text-[var(--warn-text)]">
              {t("timeline.revertBannerCount", { count: 1 })}
            </span>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <button
              type="button"
              disabled={busy()}
              onClick={() => void onStepRedo()}
              class="inline-flex items-center gap-1 rounded-lg border border-[var(--warn-border)] bg-[var(--bg-panel)]/70 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
            >
              <Show when={!busy()} fallback={<LoaderCircle size={11} class="animate-spin" />}>
                <RotateCcw size={11} class="scale-x-[-1]" />
              </Show>
              <span>{t("timeline.redoOneTurn")}</span>
            </button>

            <button
              type="button"
              disabled={busy()}
              onClick={() => void onRestoreAll()}
              class="inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-50 cursor-pointer"
            >
              <span>{t("timeline.redoAll")}</span>
            </button>
          </div>
        </div>

        <Show when={sessionDiffs().length > 0}>
          <div class="flex flex-wrap items-center gap-1.5">
            <For each={sessionDiffs().slice(0, 5)}>
              {(d) => (
                <button
                  type="button"
                  onClick={() => openDiffInInspector(d.file)}
                  class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <FileIcon node={{ path: d.file, type: "file" }} class="w-3 h-3 shrink-0" />
                  <span class="font-mono truncate max-w-[140px]">
                    {d.file.split("/").pop() || d.file}
                  </span>
                  <DiffChanges changes={{ additions: d.additions, deletions: d.deletions }} />
                </button>
              )}
            </For>
          </div>
        </Show>

        <div class="text-[11px] text-[var(--text-muted)]">
          {t("timeline.revertBannerHint")}
        </div>
      </div>
    </Show>
  );
}
