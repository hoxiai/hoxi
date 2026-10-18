// web/src/components/timeline/RevertBanner.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { GitFork, LoaderCircle, RotateCcw, Undo2 } from "lucide-solid";
import {
  forkFromMessage,
  openDiffInInspector,
  pendingRevertConfirm,
  revertToHere,
  setPendingRevertConfirm,
  undoRevert,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { t } from "../../i18n";

/**
 * Ported from kilocode RevertBanner.tsx:
 * Shows when the active session has a pending revert confirmation or is in a reverted checkpoint state,
 * displaying the reverted turn count, per-file diff stats, Step Redo, Fork, and Restore All actions.
 */
export default function RevertBanner() {
  const [busy, setBusy] = createSignal(false);

  const activeSession = createMemo(() => {
    const id = workspace.activeSessionID;
    if (!id) return null;
    return workspace.sessions.find((s) => s.id === id) ?? null;
  });

  const pendingConfirm = createMemo(() => {
    const req = pendingRevertConfirm();
    if (!req || req.sessionID !== workspace.activeSessionID) return null;
    return req;
  });

  const revertBoundary = createMemo(() => activeSession()?.revert?.messageID ?? null);

  const revertedTurnCount = createMemo(() => {
    const id = workspace.activeSessionID;
    const boundary = revertBoundary();
    if (!id || !boundary) return 0;
    const turns = workspace.messagesBySession[id] ?? [];
    const idx = turns.findIndex((t) => t.info.id === boundary);
    if (idx >= 0) {
      const userCount = turns
        .slice(idx)
        .filter((t) => t.info.role === "user").length;
      return Math.max(1, userCount);
    }
    const count = turns.filter(
      (t) => t.info.role === "user" && t.info.id >= boundary
    ).length;
    return Math.max(1, count);
  });

  const sessionDiffs = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.diffsBySession[id] ?? [] : [];
  });

  const onConfirmRevertBoth = async () => {
    const req = pendingConfirm();
    if (!req || busy()) return;
    setBusy(true);
    try {
      await revertToHere(req.sessionID, req.messageID, req.partID, { skipConfirm: true });
    } catch (err) {
      console.error("confirm revert failed:", err);
    } finally {
      setBusy(false);
    }
  };

  const onConfirmForkInstead = async () => {
    const req = pendingConfirm();
    if (!req || busy()) return;
    setBusy(true);
    try {
      await forkFromMessage(req.sessionID, req.messageID);
    } catch (err) {
      console.error("fork instead of revert failed:", err);
    } finally {
      setBusy(false);
    }
  };

  const onStepRedo = async () => {
    const id = workspace.activeSessionID;
    const boundary = revertBoundary();
    if (!id || !boundary || busy()) return;

    setBusy(true);
    try {
      const turns = workspace.messagesBySession[id] ?? [];
      const bIdx = turns.findIndex((t) => t.info.id === boundary);
      const nextUser =
        bIdx >= 0
          ? turns.slice(bIdx + 1).find((turn) => turn.info.role === "user")
          : turns.find((turn) => turn.info.role === "user" && turn.info.id > boundary);
      if (nextUser) {
        await revertToHere(id, nextUser.info.id, undefined, { skipConfirm: true });
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

  const onForkFromBoundary = async () => {
    const id = workspace.activeSessionID;
    const boundary = revertBoundary();
    if (!id || !boundary || busy()) return;
    setBusy(true);
    try {
      await forkFromMessage(id, boundary);
    } catch (err) {
      console.error("fork from revert boundary failed:", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Show when={pendingConfirm()}>
        {(req) => (
          <div class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)]/95 px-3.5 py-2.5 shadow-sm space-y-2">
            <div class="flex items-center justify-between gap-2 flex-wrap">
              <div class="flex items-center gap-2 min-w-0">
                <Undo2 size={14} class="text-[var(--warn-text)] shrink-0" />
                <span class="text-[12px] font-medium text-[var(--warn-text)]">
                  {t("timeline.revertConfirmWithFiles", {
                    count: Math.max(1, req().affectedFiles.length),
                  })}
                </span>
              </div>

              <div class="flex items-center gap-1.5 shrink-0">
                <button
                  type="button"
                  disabled={busy()}
                  onClick={() => void onConfirmRevertBoth()}
                  class="inline-flex items-center gap-1 rounded-lg bg-[var(--warn-text)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-50 cursor-pointer"
                >
                  <Show when={!busy()} fallback={<LoaderCircle size={11} class="animate-spin" />}>
                    <Undo2 size={11} />
                  </Show>
                  <span>{t("timeline.revertConfirmBothBtn")}</span>
                </button>

                <button
                  type="button"
                  disabled={busy()}
                  onClick={() => void onConfirmForkInstead()}
                  class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
                >
                  <GitFork size={11} />
                  <span>{t("timeline.revertForkKeepCodeBtn")}</span>
                </button>

                <button
                  type="button"
                  disabled={busy()}
                  onClick={() => setPendingRevertConfirm(null)}
                  class="inline-flex items-center rounded-lg px-2 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <span>{t("common.cancel")}</span>
                </button>
              </div>
            </div>

            <Show when={req().affectedFiles.length > 0}>
              <div class="flex flex-wrap items-center gap-1.5">
                <For each={req().affectedFiles.slice(0, 6)}>
                  {(file) => (
                    <button
                      type="button"
                      onClick={() => openDiffInInspector(file)}
                      class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <FileIcon node={{ path: file, type: "file" }} class="w-3 h-3 shrink-0" />
                      <span class="font-mono truncate max-w-[160px]">
                        {file.split("/").pop() || file}
                      </span>
                    </button>
                  )}
                </For>
              </div>
            </Show>
          </div>
        )}
      </Show>

      <Show when={revertBoundary()}>
        <div class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)]/90 px-3.5 py-2.5 shadow-xs space-y-2">
          <div class="flex items-center justify-between gap-2">
            <div class="flex items-center gap-2 min-w-0">
              <Undo2 size={14} class="text-[var(--warn-text)] shrink-0" />
              <span class="truncate text-[12px] font-medium text-[var(--warn-text)]">
                {t("timeline.revertBannerCount", { count: revertedTurnCount() })}
              </span>
            </div>

            <div class="flex items-center gap-1.5 shrink-0">
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onForkFromBoundary()}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--warn-border)] bg-[var(--bg-panel)]/70 px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
                title={t("timeline.forkTooltip")}
              >
                <GitFork size={11} />
                <span>{t("timeline.forkBtn")}</span>
              </button>

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
    </>
  );
}
