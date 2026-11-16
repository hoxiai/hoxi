// web/src/components/timeline/SessionSkeleton.tsx
import { createMemo, createSignal, For } from "solid-js";
import { t } from "../../i18n";
import { submitSessionPrompt, workspace } from "../../store/workspace";
import { Icon } from "../icons";
import { basename } from "./message-stream-parts";
import ParticleLogo from "./ParticleLogo";

/**
 * Conversation skeleton shown while a session's messages are being fetched
 * for the first time, rendering the Hoxi particle wordmark with a gentle wave shimmer.
 */
export function SessionSkeleton() {
  return (
    <div
      aria-busy="true"
      aria-live="polite"
      class="my-auto flex w-full flex-1 flex-col items-center justify-center py-8 select-none"
    >
      <ParticleLogo loading />
    </div>
  );
}

/**
 * Kilo-style spacious Welcome Hero shown only when a session is genuinely empty.
 */
export function SessionWelcomeHero() {
  const [launchingPrompt, setLaunchingPrompt] = createSignal(false);
  const projectName = createMemo(() => basename(workspace.rootDir));

  const welcomeTitleParts = createMemo(() => {
    const full = t("timeline.welcomeQuestion", { project: "\u0000" });
    const [before = "", after = ""] = full.split("\u0000");
    return { before, after };
  });

  const quickSuggestions = createMemo(() => [
    t("timeline.welcomePrompt1"),
    t("timeline.welcomePrompt2"),
    t("timeline.welcomePrompt3"),
  ]);

  const onRunSuggestion = async (prompt: string) => {
    if (launchingPrompt()) return;
    setLaunchingPrompt(true);
    try {
      await submitSessionPrompt(prompt, workspace.activeSessionID);
    } catch (err) {
      console.error("suggestion prompt failed:", err);
    } finally {
      setLaunchingPrompt(false);
    }
  };

  return (
    <div class="my-auto flex flex-col items-center justify-center py-12 text-center">
      <div class="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs">
        <svg viewBox="0 0 64 64" fill="none" class="h-8 w-8">
          <path d="M32 8L52 21H44L32 13L20 21H12L32 8Z" fill="currentColor" />
          <rect x="20" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
          <rect x="37.5" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
          <path
            d="M19 42.5C23 50 41 50 45 42.5"
            stroke="currentColor"
            stroke-width="5.5"
            stroke-linecap="round"
          />
        </svg>
      </div>

      <h1 class="text-[22px] font-medium tracking-tight text-[var(--text-primary)]">
        {welcomeTitleParts().before}
        <span class="border-b border-dotted border-[var(--text-muted)] font-semibold">
          {projectName()}
        </span>
        {welcomeTitleParts().after}
      </h1>
      <p class="mt-2 max-w-lg text-[13px] text-[var(--text-muted)]">
        {t("timeline.welcomeSubtitle")}
      </p>

      <div class="mt-7 flex flex-wrap items-center justify-center gap-2 max-w-[50rem]">
        <For each={quickSuggestions()}>
          {(suggestion) => (
            <button
              type="button"
              disabled={launchingPrompt()}
              onClick={() => void onRunSuggestion(suggestion)}
              class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3.5 py-1.5 text-[13px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-all cursor-pointer disabled:opacity-50"
            >
              <span>{suggestion}</span>
              <Icon name="arrow-up" size="small" class="rotate-45 text-[var(--text-faint)]" />
            </button>
          )}
        </For>
      </div>
    </div>
  );
}
