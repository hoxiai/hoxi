// web/src/components/settings/OldSessionsCleanup.tsx
// Manual, preview-first deletion of conversations idle for N days. Nothing is
// deleted until the user confirms the exact selection.
import { createMemo, createSignal, For, Show } from "solid-js";
import { Archive, GitBranch, Search, Trash2, Users } from "lucide-solid";
import type {
  OldSessionCandidate,
  OldSessionSkipReason,
  OldSessionsDeleteResult,
  OldSessionsPreview,
  SystemStorageStats,
} from "../../services/api";
import { deleteOldSessions, previewOldSessions } from "../../store/workspace";
import { locale, t, type TxKey } from "../../i18n";
import { Spinner } from "../icons";
import { SegmentedPills } from "./setting-controls";
import { formatBytes } from "./storage-format";

type DayOption = "30" | "60" | "90" | "180";
const DAY_OPTIONS: DayOption[] = ["30", "60", "90", "180"];

const REASON_KEYS: Record<OldSessionSkipReason, TxKey> = {
  notEligible: "settings.oldSessionsReasonNotEligible",
  busy: "settings.oldSessionsReasonBusy",
  worktreeDirty: "settings.oldSessionsReasonWorktreeDirty",
  worktreeFailed: "settings.oldSessionsReasonWorktreeFailed",
  deleteFailed: "settings.oldSessionsReasonDeleteFailed",
};

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function OldSessionsCleanup(props: {
  onToast: (msg: string) => void;
  onStorage: (stats: SystemStorageStats) => void;
}) {
  const [days, setDays] = createSignal<DayOption>("30");
  const [preview, setPreview] = createSignal<OldSessionsPreview | null>(null);
  const [selected, setSelected] = createSignal<Set<string>>(new Set());
  const [finding, setFinding] = createSignal(false);
  const [confirming, setConfirming] = createSignal(false);
  const [deleting, setDeleting] = createSignal(false);
  const [result, setResult] = createSignal<OldSessionsDeleteResult | null>(null);
  // Drops a preview that resolves after the user already asked for another.
  let generation = 0;

  const sessions = () => preview()?.sessions ?? [];
  const selectedSessions = createMemo(() => sessions().filter((s) => selected().has(s.id)));
  const selectedBytes = () => selectedSessions().reduce((sum, s) => sum + s.bytes, 0);
  const allSelected = () => sessions().length > 0 && selectedSessions().length === sessions().length;
  const busy = () => finding() || deleting();

  const find = async () => {
    const gen = ++generation;
    setFinding(true);
    setConfirming(false);
    setResult(null);
    try {
      const res = await previewOldSessions(Number(days()));
      if (gen !== generation) return;
      setPreview(res);
      setSelected(new Set(res.sessions.map((s) => s.id)));
    } catch (err) {
      if (gen === generation) props.onToast(t("settings.oldSessionsFailed", { error: errorText(err) }));
    } finally {
      if (gen === generation) setFinding(false);
    }
  };

  const changeDays = (next: DayOption) => {
    generation++;
    setDays(next);
    setPreview(null);
    setSelected(new Set<string>());
    setConfirming(false);
    setResult(null);
    setFinding(false);
  };

  const toggle = (id: string) => {
    const next = new Set(selected());
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
    setConfirming(false);
  };

  const toggleAll = () => {
    setSelected(allSelected() ? new Set<string>() : new Set(sessions().map((s) => s.id)));
    setConfirming(false);
  };

  const remove = async () => {
    const current = preview();
    const ids = selectedSessions().map((s) => s.id);
    if (!current || ids.length === 0 || deleting()) return;
    generation++;
    setDeleting(true);
    try {
      const res = await deleteOldSessions(current.olderThanDays, ids);
      props.onStorage(res.storage);
      setResult(res);
      const gone = new Set(res.deleted);
      setPreview({
        ...current,
        sessions: current.sessions.filter((s) => !gone.has(s.id)),
        totalBytes: current.sessions.filter((s) => !gone.has(s.id)).reduce((sum, s) => sum + s.bytes, 0),
      });
      setSelected(new Set<string>());
      props.onToast(
        t("settings.oldSessionsDeleted", { count: res.deleted.length, size: formatBytes(res.freedBytes) })
      );
    } catch (err) {
      props.onToast(t("settings.oldSessionsFailed", { error: errorText(err) }));
    } finally {
      setDeleting(false);
      setConfirming(false);
    }
  };

  const titleOf = (id: string) =>
    preview()?.sessions.find((s) => s.id === id)?.title || t("settings.oldSessionsUntitled");

  const lastActive = (s: OldSessionCandidate) =>
    new Date(s.updatedAt).toLocaleDateString(locale(), { year: "numeric", month: "short", day: "numeric" });

  return (
    <div class="space-y-3 px-5 py-4">
      <div class="space-y-1">
        <div class="flex items-center gap-2 text-[13px] font-medium text-[var(--text-primary)]">
          <Trash2 size={14} class="text-[var(--text-muted)]" />
          {t("settings.oldSessionsTitle")}
        </div>
        <p class="text-[11.5px] leading-relaxed text-[var(--text-muted)]">{t("settings.oldSessionsDesc")}</p>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <span class="text-[12px] text-[var(--text-muted)]">{t("settings.oldSessionsIdleFor")}</span>
        <SegmentedPills
          value={days()}
          options={DAY_OPTIONS.map((d) => ({ id: d, label: () => t("settings.oldSessionsDays", { days: d }) }))}
          onChange={(d) => !busy() && changeDays(d)}
        />
        <button
          type="button"
          disabled={busy()}
          onClick={() => void find()}
          class="hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3.5 py-1.5 text-[13px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:opacity-50 disabled:cursor-wait"
        >
          <Show when={finding()} fallback={<Search size={14} />}>
            <Spinner class="h-3.5 w-3.5" />
          </Show>
          {finding() ? t("settings.oldSessionsFinding") : t("settings.oldSessionsFind")}
        </button>
      </div>

      <Show when={preview()}>
        {(p) => (
          <div class="space-y-2.5">
            <div class="flex flex-wrap items-center justify-between gap-2 text-[12px]">
              <span class="text-[var(--text-secondary)]">
                {p().sessions.length === 0
                  ? t("settings.oldSessionsNone", { days: p().olderThanDays })
                  : t("settings.oldSessionsFound", { count: p().sessions.length, size: formatBytes(p().totalBytes) })}
              </span>
              <Show when={p().busySkipped > 0}>
                <span class="text-[var(--text-faint)]">
                  {t("settings.oldSessionsBusySkipped", { count: p().busySkipped })}
                </span>
              </Show>
            </div>

            <Show when={p().sessions.length > 0}>
              <div class="overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]">
                <label class="flex cursor-pointer items-center gap-2.5 border-b border-[var(--border-subtle)]/60 px-3.5 py-2 text-[12px] font-medium text-[var(--text-secondary)]">
                  <input type="checkbox" checked={allSelected()} disabled={deleting()} onChange={toggleAll} />
                  {t("settings.oldSessionsSelectAll")}
                </label>
                <div class="max-h-72 divide-y divide-[var(--border-subtle)]/50 overflow-y-auto">
                  <For each={p().sessions}>
                    {(s) => (
                      <label class="flex cursor-pointer items-start gap-2.5 px-3.5 py-2 hover:bg-[var(--bg-hover)]">
                        <input
                          type="checkbox"
                          class="mt-0.5"
                          checked={selected().has(s.id)}
                          disabled={deleting()}
                          onChange={() => toggle(s.id)}
                        />
                        <div class="min-w-0 flex-1 space-y-0.5">
                          <div class="truncate text-[12.5px] text-[var(--text-primary)]">
                            {s.title || t("settings.oldSessionsUntitled")}
                          </div>
                          <div class="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[11px] text-[var(--text-faint)]">
                            <span>{t("settings.oldSessionsLastActive", { date: lastActive(s) })}</span>
                            <Show when={s.directory}>
                              <span class="max-w-60 truncate font-mono" title={s.directory}>
                                {s.directory!.split(/[\\/]/).filter(Boolean).pop()}
                              </span>
                            </Show>
                            <Show when={s.children > 0}>
                              <span class="inline-flex items-center gap-1">
                                <Users size={11} />
                                {t("settings.oldSessionsChildren", { count: s.children })}
                              </span>
                            </Show>
                            <Show when={s.archived}>
                              <span class="inline-flex items-center gap-1">
                                <Archive size={11} />
                                {t("settings.oldSessionsArchived")}
                              </span>
                            </Show>
                            <Show when={s.worktree}>
                              <span class="inline-flex items-center gap-1">
                                <GitBranch size={11} />
                                {t("settings.oldSessionsWorktree")}
                              </span>
                            </Show>
                          </div>
                        </div>
                        <span class="shrink-0 font-mono text-[11.5px] text-[var(--text-secondary)]">
                          {formatBytes(s.bytes)}
                        </span>
                      </label>
                    )}
                  </For>
                </div>
              </div>

              <Show when={p().sessions.some((s) => s.worktree)}>
                <p class="text-[11px] leading-relaxed text-[var(--text-faint)]">{t("settings.oldSessionsWorktreeNote")}</p>
              </Show>

              <Show
                when={confirming()}
                fallback={
                  <button
                    type="button"
                    disabled={busy() || selectedSessions().length === 0}
                    onClick={() => setConfirming(true)}
                    class="hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-red-500/30 bg-red-500/10 px-3.5 py-1.5 text-[13px] font-medium text-red-500 hover:bg-red-500/15 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <Trash2 size={14} />
                    {t("settings.oldSessionsDeleteSelected", { count: selectedSessions().length })}
                  </button>
                }
              >
                <div class="flex flex-wrap items-center gap-2.5 rounded-xl border border-red-500/30 bg-red-500/10 px-3.5 py-2.5">
                  <span class="min-w-0 flex-1 text-[12.5px] text-[var(--text-primary)]">
                    {t("settings.oldSessionsConfirm", {
                      count: selectedSessions().length,
                      size: formatBytes(selectedBytes()),
                    })}
                  </span>
                  <button
                    type="button"
                    disabled={deleting()}
                    onClick={() => setConfirming(false)}
                    class="rounded-lg px-3 py-1.5 text-[12.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:opacity-50"
                  >
                    {t("settings.oldSessionsCancel")}
                  </button>
                  <button
                    type="button"
                    disabled={deleting()}
                    onClick={() => void remove()}
                    class="hoxi-press inline-flex items-center gap-1.5 rounded-lg bg-red-500 px-3 py-1.5 text-[12.5px] font-medium text-white hover:opacity-90 cursor-pointer disabled:opacity-60 disabled:cursor-wait"
                  >
                    <Show when={deleting()} fallback={<Trash2 size={13} />}>
                      <Spinner class="h-3.5 w-3.5" />
                    </Show>
                    {deleting() ? t("settings.oldSessionsDeleting") : t("settings.oldSessionsConfirmBtn")}
                  </button>
                </div>
              </Show>
            </Show>
          </div>
        )}
      </Show>

      <Show when={result()}>
        {(r) => (
          <div class="space-y-1.5">
            <Show when={r().vacuumDeferred}>
              <p class="text-[11.5px] text-[var(--text-faint)]">{t("settings.oldSessionsVacuumDeferred")}</p>
            </Show>
            <Show when={r().skipped.length > 0}>
              <div class="space-y-1 rounded-xl border border-amber-500/25 bg-amber-500/10 px-3.5 py-2.5">
                <div class="text-[12px] font-medium text-amber-600 dark:text-amber-400">
                  {t("settings.oldSessionsSkippedTitle", { count: r().skipped.length })}
                </div>
                <For each={r().skipped}>
                  {(skip) => (
                    <div class="flex items-center justify-between gap-3 text-[11.5px] text-[var(--text-secondary)]">
                      <span class="min-w-0 truncate">{titleOf(skip.id)}</span>
                      <span class="shrink-0 text-[var(--text-faint)]" title={skip.error}>
                        {t(REASON_KEYS[skip.reason] ?? "settings.oldSessionsReasonDeleteFailed")}
                      </span>
                    </div>
                  )}
                </For>
              </div>
            </Show>
          </div>
        )}
      </Show>
    </div>
  );
}
