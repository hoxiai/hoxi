import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, LoaderCircle, ShieldAlert, Terminal, X, Zap } from "lucide-solid";
import type { PermissionRequest } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import { sessionDirectory, workspace } from "../../store/workspace";
import { FileDiffBlock } from "../inspector/PierreDiffViewer";
import { t } from "../../i18n";

/**
 * Ported from kilocode PermissionDock.tsx:
 * Supports command preview, diff preview, and Reject-with-Feedback steering.
 */
export default function ApprovalGateCard(props: { request: PermissionRequest }) {
  const [busy, setBusy] = createSignal<null | "once" | "always" | "reject">(null);
  const [rejecting, setRejecting] = createSignal(false);
  const [feedback, setFeedback] = createSignal("");
  const [error, setError] = createSignal<string | null>(null);

  const command = createMemo(() => {
    const meta = props.request.metadata ?? {};
    if (typeof meta.command === "string" && meta.command.trim()) return meta.command.trim();
    if (props.request.permission === "bash" && props.request.patterns?.length) {
      return props.request.patterns.join("\n");
    }
    return "";
  });

  const diffPatch = createMemo(() => {
    const meta = props.request.metadata ?? {};
    if (typeof meta.filediff?.patch === "string" && meta.filediff.patch.trim()) {
      return meta.filediff.patch;
    }
    if (typeof meta.diff === "string" && meta.diff.trim()) {
      return meta.diff;
    }
    return null;
  });

  const reply = async (mode: "once" | "always" | "reject") => {
    if (busy()) return;
    setBusy(mode);
    setError(null);
    const dir = sessionDirectory(props.request.sessionID);
    try {
      await hoxiAPI.replyPermission(props.request.id, mode, dir);
      // If rejecting with steering feedback, send the feedback prompt right after
      const note = feedback().trim();
      if (mode === "reject" && note && props.request.sessionID) {
        await hoxiAPI.sendPromptAsync(props.request.sessionID, note, {
          agent: workspace.activeMode,
          model: workspace.selectedModel || undefined,
          variant: workspace.reasoningEffort || undefined,
          directory: dir,
        });
      }
    } catch (err) {
      console.error("permission reply failed:", err);
      setError(String(err));
      setBusy(null);
    }
  };

  return (
    <div class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] overflow-hidden shadow-sm">
      <div class="flex items-center gap-2 px-3.5 py-2 border-b border-[var(--warn-border)]">
        <ShieldAlert size={14} class="text-[var(--warn-text)] shrink-0" />
        <span class="text-[12px] font-semibold text-[var(--warn-text)]">
          {t("approval.title")}
        </span>
        <span class="ml-auto rounded-md bg-[var(--bg-panel)]/70 px-2 py-0.5 font-mono text-[10.5px] text-[var(--warn-text)]">
          {props.request.permission || "tool"}
        </span>
      </div>

      <div class="px-3.5 py-2.5 space-y-2.5 max-h-[260px] overflow-y-auto">
        <Show
          when={command()}
          fallback={
            <Show when={props.request.patterns?.length}>
              <div class="space-y-1">
                <For each={props.request.patterns}>
                  {(pattern) => (
                    <pre class="overflow-auto rounded-lg bg-[var(--bg-code)] px-2.5 py-1.5 font-mono text-[11px] leading-relaxed text-[var(--text-primary)] selectable-text whitespace-pre-wrap break-all">
                      {pattern}
                    </pre>
                  )}
                </For>
              </div>
            </Show>
          }
        >
          <div class="flex items-start gap-2 rounded-lg bg-[var(--bg-code)] px-3 py-2 font-mono text-[11.5px] text-[var(--text-primary)]">
            <Terminal size={13} class="mt-0.5 text-[var(--accent-primary)] shrink-0" />
            <pre class="whitespace-pre-wrap break-all selectable-text">{command()}</pre>
          </div>
        </Show>

        <Show when={diffPatch()}>
          <FileDiffBlock patch={diffPatch()!} diffStyle="unified" maxHeight={180} />
        </Show>

        <Show when={rejecting()}>
          <div class="space-y-1">
            <textarea
              rows={2}
              value={feedback()}
              onInput={(ev) => setFeedback(ev.currentTarget.value)}
              onKeyDown={(ev) => {
                if (ev.key === "Escape") {
                  ev.preventDefault();
                  setRejecting(false);
                } else if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
                  ev.preventDefault();
                  void reply("reject");
                }
              }}
              placeholder={t("approval.rejectFeedbackPlaceholder")}
              class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
            />
          </div>
        </Show>

        <Show when={error()}>
          <div class="text-[11px] text-[var(--danger-text)] font-mono">{error()}</div>
        </Show>
      </div>

      <div class="flex flex-wrap items-center gap-2 px-3.5 py-2 border-t border-[var(--warn-border)] bg-[var(--bg-panel)]/40">
        <Show
          when={rejecting()}
          fallback={
            <>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--ok-border)] bg-[var(--ok-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--ok-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => void reply("once")}
              >
                <Show when={busy() !== "once"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                  <Check size={12} />
                </Show>
                {t("approval.allowOnce")}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => void reply("always")}
              >
                <Show when={busy() !== "always"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                  <Zap size={12} />
                </Show>
                {t("approval.allowAlways")}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] hover:opacity-90 px-3 py-1 text-[11.5px] font-medium text-[var(--danger-text)] disabled:opacity-50 transition-opacity cursor-pointer"
                disabled={!!busy()}
                onClick={() => setRejecting(true)}
              >
                <X size={12} />
                {t("approval.reject")}…
              </button>
            </>
          }
        >
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--status-danger)] text-white hover:opacity-90 px-3 py-1 text-[11.5px] font-medium disabled:opacity-50 transition-opacity cursor-pointer"
            disabled={!!busy()}
            onClick={() => void reply("reject")}
          >
            <Show when={busy() !== "reject"} fallback={<LoaderCircle size={12} class="animate-spin" />}>
              <X size={12} />
            </Show>
            {feedback().trim()
              ? t("approval.rejectWithFeedback")
              : t("approval.reject")}
          </button>
          <button
            type="button"
            class="inline-flex items-center rounded-lg px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            disabled={!!busy()}
            onClick={() => setRejecting(false)}
          >
            {t("common.cancel")}
          </button>
        </Show>
      </div>
    </div>
  );
}
