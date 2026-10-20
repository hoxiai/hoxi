// web/src/components/inspector/ai-commit-fallback-toast.tsx
import { Show } from "solid-js";
import { X } from "lucide-solid";
import { t } from "../../i18n";
import { dismissAICommitFallback, hasAICommitFallbackNotice } from "../../store/workspace";

export default function AICommitFallbackToast() {
  return (
    <Show when={hasAICommitFallbackNotice()}>
      <div class="hoxi-view-enter shrink-0 flex items-center justify-between gap-2 px-3 py-1.5 bg-[var(--warn-surface)] text-[11px] font-sans text-[var(--warn-text)] border-b border-[var(--warn-border)]">
        <span class="truncate">{t("inspector.diffs.aiCommitFallback")}</span>
        <button
          type="button"
          onClick={() => dismissAICommitFallback()}
          class="hoxi-press shrink-0 inline-flex items-center rounded-md border border-[var(--warn-border)] bg-[var(--bg-panel)]/80 p-0.5 hover:bg-[var(--bg-panel)] cursor-pointer"
          aria-label={t("inspector.diffs.aiCommitFallback")}
        >
          <X size={10} />
        </button>
      </div>
    </Show>
  );
}
