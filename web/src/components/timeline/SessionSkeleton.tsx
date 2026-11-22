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
        <svg viewBox="10 10 44 44" fill="currentColor" class="h-8 w-8">
          <path d="M13.41 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 13.07a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 16.86a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 20.64a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 24.43a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M24.77 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M28.56 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M32.34 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M36.13 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 28.21a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M24.77 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M28.56 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M32.34 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M36.13 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 32.00a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M24.77 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M28.56 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M32.34 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M36.13 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 35.79a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 39.57a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 43.36a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 47.14a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M13.41 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M17.20 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M20.98 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M39.91 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M43.70 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0 M47.48 50.93a1.55 1.55 0 1 0 3.10 0a1.55 1.55 0 1 0 -3.10 0" />
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
