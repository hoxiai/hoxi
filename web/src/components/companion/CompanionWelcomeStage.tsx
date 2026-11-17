// web/src/components/companion/CompanionWelcomeStage.tsx
import { ArrowRightLeft, FolderGit2, ShieldAlert, Sparkles } from "lucide-solid";
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import {
  appSettings,
  CELEBRATE_WINDOW_MS,
  companionState,
  deriveCompanionSnapshot,
  focusFirstAttentionSession,
  openHandoffModal,
  POKE_BUBBLE_WINDOW_MS,
  pokeCompanion,
  selectSession,
  setSessionSandboxMode,
  workspace,
} from "../../store/workspace";
import {
  CompanionAvatar,
  formatCompanionTokensText,
  resolveCompanionDialogue,
  resolveCompanionMoodBadge,
  resolveCompanionPersonaName,
  type MoodBadgeTone,
} from "./companion-art";

function badgeToneClass(tone: MoodBadgeTone): string {
  switch (tone) {
    case "danger":
      return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
    case "warn":
      return "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]";
    case "info":
      return "border-[var(--info-border)] bg-[var(--info-surface)] text-[var(--info-text)]";
    case "ok":
      return "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]";
    case "accent":
      return "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]";
  }
}

export default function CompanionWelcomeStage() {
  const [clockTick, setClockTick] = createSignal(Date.now());

  createEffect(() => {
    const pokeAt = companionState.lastPokeAt;
    const activeSid = workspace.activeSessionID;
    const finishedAt = activeSid ? workspace.sessionFinishedAt[activeSid] || 0 : 0;
    const now = Date.now();
    let nextWakeMs = 60_000;
    if (pokeAt > 0 && now - pokeAt < POKE_BUBBLE_WINDOW_MS) {
      nextWakeMs = Math.min(nextWakeMs, POKE_BUBBLE_WINDOW_MS - (now - pokeAt) + 40);
    }
    if (finishedAt > 0 && now - finishedAt < CELEBRATE_WINDOW_MS) {
      nextWakeMs = Math.min(nextWakeMs, CELEBRATE_WINDOW_MS - (now - finishedAt) + 40);
    }
    const timer = setTimeout(() => setClockTick(Date.now()), Math.max(200, nextWakeMs));
    onCleanup(() => clearTimeout(timer));
  });

  const snap = createMemo(() => deriveCompanionSnapshot(clockTick()));
  const badge = createMemo(() => resolveCompanionMoodBadge(snap()));
  const dialogue = createMemo(() =>
    resolveCompanionDialogue(snap(), appSettings.colorScheme)
  );
  const isWorktree = () => appSettings.defaultSandboxMode === "worktree";

  const onJumpGate = () => {
    const target = snap().targetSessionID;
    if (target) {
      void selectSession(target);
      return;
    }
    focusFirstAttentionSession();
  };

  return (
    <div class="mx-auto mb-3.5 w-full max-w-[var(--thread-max-width)] px-6">
      <div
        class={`hoxi-companion-glass relative flex flex-col sm:flex-row items-center gap-4 rounded-2xl px-5 py-4 transition-colors ${
          snap().mood === "gate-danger"
            ? "border-[var(--danger-border)]"
            : ""
        }`}
      >
        <button
          type="button"
          onClick={() => {
            pokeCompanion();
            setClockTick(Date.now());
          }}
          title={t("companion.actionPoke")}
          class="group relative shrink-0 rounded-2xl p-1 transition-transform active:scale-95 cursor-pointer"
        >
          <CompanionAvatar
            persona={companionState.persona}
            mood={snap().mood}
            customUrl={companionState.customAvatarObjectUrl}
            size="stage"
          />
        </button>

        <div class="min-w-0 flex-1 space-y-2 text-center sm:text-left">
          <div class="flex flex-wrap items-center justify-center sm:justify-start gap-2">
            <span class="text-[14px] font-semibold text-[var(--text-primary)]">
              {resolveCompanionPersonaName(companionState.persona)}
            </span>
            <span
              class={`inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium ${badgeToneClass(
                badge().tone
              )}`}
            >
              {badge().label}
            </span>
            <span class="inline-flex items-center gap-1.5 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 font-mono text-[11px] font-medium text-[var(--accent-text)]">
              <span>{t("companion.levelBadge", { level: snap().tokenLevel })}</span>
              <span class="opacity-65">·</span>
              <span>{formatCompanionTokensText(snap().todayTokens)}</span>
            </span>
          </div>

          <p class="text-[13px] leading-relaxed text-[var(--text-secondary)]">
            {dialogue()}
          </p>

          <div class="flex flex-wrap items-center justify-center sm:justify-start gap-1.5 pt-0.5">
            <Show when={snap().mood === "gate-danger" || snap().mood === "gate-approval"}>
              <button
                type="button"
                onClick={onJumpGate}
                class="hoxi-press inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1 text-[12px] font-medium text-[var(--danger-text)] cursor-pointer"
              >
                <ShieldAlert size={13} />
                <span>{t("companion.actionJumpApproval")}</span>
              </button>
            </Show>

            <button
              type="button"
              onClick={() => openHandoffModal()}
              class="hoxi-press inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <ArrowRightLeft size={12.5} class="text-[var(--accent-primary)]" />
              <span>{t("companion.actionHandoff")}</span>
            </button>

            <button
              type="button"
              onClick={() => setSessionSandboxMode(isWorktree() ? "local" : "worktree")}
              class={`hoxi-press inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-[12px] font-medium cursor-pointer ${
                isWorktree()
                  ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                  : "border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <FolderGit2 size={12.5} />
              <span>
                {isWorktree()
                  ? t("companion.actionWorktreeActive")
                  : t("companion.actionSwitchWorktree")}
              </span>
            </button>

            <button
              type="button"
              onClick={() => {
                pokeCompanion();
                setClockTick(Date.now());
              }}
              class="hoxi-press inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Sparkles size={12} class="text-[var(--accent-primary)]" />
              <span>{t("companion.actionPoke")}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
