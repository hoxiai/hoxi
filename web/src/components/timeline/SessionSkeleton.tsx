// web/src/components/timeline/SessionSkeleton.tsx
import { createMemo, createSignal, For } from "solid-js";
import { t } from "../../i18n";
import { submitSessionPrompt, workspace } from "../../store/workspace";
import { Icon } from "../icons";
import { basename } from "./message-stream-parts";

/**
 * Conversation skeleton shown while a session's messages are being fetched
 * for the first time, preventing the Welcome Hero from flashing on session switch.
 */
export function SessionSkeleton() {
  return (
    <div
      aria-busy="true"
      aria-live="polite"
      class="flex flex-col select-none pointer-events-none py-1 animate-pulse"
    >
      {/* Turn 1: User Prompt Bubble (Right-Aligned) */}
      <div class="flex flex-col items-end">
        <div class="w-[54%] max-w-[420px] rounded-2xl bg-[var(--bg-elevated)] border border-[var(--border-subtle)] px-4 py-3.5 shadow-2xs space-y-2.5">
          <div class="h-3.5 w-[90%] rounded-md bg-[var(--bg-hover)]" />
          <div class="h-3.5 w-[64%] rounded-md bg-[var(--bg-hover)]" />
        </div>
      </div>

      {/* Turn 1: Assistant Work + Response (Left-Aligned) */}
      <div class="mt-6 flex flex-col gap-y-3.5 px-1">
        {/* Compact folded tool/reasoning activity pill */}
        <div class="inline-flex items-center gap-2 self-start rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5">
          <div class="h-3 w-3 rounded-xs bg-[var(--bg-hover)]" />
          <div class="h-3 w-36 rounded-xs bg-[var(--bg-hover)]" />
        </div>

        {/* Assistant markdown text lines */}
        <div class="space-y-2.5 pt-0.5">
          <div class="h-3.5 w-[84%] rounded-md bg-[var(--bg-inset)]" />
          <div class="h-3.5 w-[76%] rounded-md bg-[var(--bg-inset)]" />
          <div class="h-3.5 w-[58%] rounded-md bg-[var(--bg-inset)]" />
        </div>

        {/* Simulated tool / code card */}
        <div class="mt-1 max-w-2xl rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/65 p-3.5 space-y-2.5">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <div class="h-3.5 w-3.5 rounded-xs bg-[var(--bg-hover)]" />
              <div class="h-3 w-44 rounded-xs bg-[var(--bg-hover)]" />
            </div>
            <div class="h-3 w-12 rounded-xs bg-[var(--bg-hover)]" />
          </div>
          <div class="space-y-1.5 pt-1">
            <div class="h-2.5 w-[92%] rounded-xs bg-[var(--bg-inset)]" />
            <div class="h-2.5 w-[78%] rounded-xs bg-[var(--bg-inset)]" />
          </div>
        </div>
      </div>

      {/* Turn 2: Follow-up User Prompt + Assistant Response (Subtle fade) */}
      <div class="mt-7 flex flex-col items-end opacity-75">
        <div class="w-[38%] max-w-[300px] rounded-2xl bg-[var(--bg-elevated)] border border-[var(--border-subtle)] px-4 py-3 shadow-2xs">
          <div class="h-3.5 w-[82%] rounded-md bg-[var(--bg-hover)]" />
        </div>
      </div>

      <div class="mt-6 flex flex-col gap-y-3.5 px-1 opacity-65">
        <div class="inline-flex items-center gap-2 self-start rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5">
          <div class="h-3 w-3 rounded-xs bg-[var(--bg-hover)]" />
          <div class="h-3 w-28 rounded-xs bg-[var(--bg-hover)]" />
        </div>
        <div class="space-y-2.5 pt-0.5">
          <div class="h-3.5 w-[72%] rounded-md bg-[var(--bg-inset)]" />
          <div class="h-3.5 w-[48%] rounded-md bg-[var(--bg-inset)]" />
        </div>
      </div>
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
      <p class="mt-2 max-w-lg text-[13.5px] text-[var(--text-muted)]">
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
