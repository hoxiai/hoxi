// web/src/components/timeline/CommandSandboxToggle.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { LoaderCircle, ShieldCheck, ShieldOff } from "lucide-solid";
import { t } from "../../i18n";
import {
  fetchSandboxSession,
  updateSandboxSession,
  type SandboxSessionState,
} from "../../store/workspace";

export default function CommandSandboxToggle(props: {
  sessionID?: string | null;
  directory?: string | null;
  busy: boolean;
  onStateChange?: (state: { enabled: boolean; available: boolean }) => void;
}) {
  const [state, setState] = createSignal<SandboxSessionState | null>(null);
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const report = (next: SandboxSessionState | null) => {
    props.onStateChange?.({
      enabled: Boolean(next?.enabled),
      available: Boolean(next?.available ?? true),
    });
  };

  createEffect(() => {
    const id = props.sessionID;
    setError(null);
    if (!id) {
      setState(null);
      report(null);
      return;
    }
    let stale = false;
    fetchSandboxSession(id)
      .then((next) => {
        if (stale) return;
        setState(next);
        report(next);
      })
      .catch(() => {
        if (stale) return;
        setState(null);
        report(null);
      });
    onCleanup(() => {
      stale = true;
    });
  });

  const enabled = createMemo(() => Boolean(state()?.enabled));
  const unavailable = createMemo(
    () => state() !== null && !state()?.available
  );
  const disabled = createMemo(
    () => !props.sessionID || props.busy || saving() || unavailable()
  );

  const description = createMemo(() => {
    if (unavailable()) return state()?.reason || t("timeline.commandSandboxUnavailable");
    if (props.busy) return t("timeline.commandSandboxBusy");
    return enabled()
      ? t("timeline.commandSandboxDisableTip")
      : t("timeline.commandSandboxEnableTip");
  });

  const toggle = async () => {
    const id = props.sessionID;
    if (!id || disabled()) return;
    setSaving(true);
    setError(null);
    try {
      const next = await updateSandboxSession({
        sessionID: id,
        directory: props.directory || undefined,
        enabled: !enabled(),
      });
      if (props.sessionID === id) {
        setState(next);
        report(next);
      }
    } catch (err) {
      if (props.sessionID === id) setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Show when={props.sessionID}>
      <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)]">
        <button
          type="button"
          onClick={() => void toggle()}
          disabled={disabled()}
          title={t("timeline.commandSandboxTitle")}
          class="w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed hover:bg-[var(--bg-hover)]"
        >
          <div
            class={`mt-0.5 p-1 rounded-lg shrink-0 ${
              enabled()
                ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
            }`}
          >
            <Show
              when={!saving()}
              fallback={<LoaderCircle size={12} class="animate-spin" />}
            >
              <Show when={enabled()} fallback={<ShieldOff size={12} />}>
                <ShieldCheck size={12} />
              </Show>
            </Show>
          </div>

          <div class="flex-1 min-w-0">
            <div class="flex items-center justify-between gap-2">
              <span class="text-[12px] font-medium">{t("timeline.commandSandboxTitle")}</span>
              <span
                class={`shrink-0 rounded px-1.5 py-[1px] text-[11px] font-medium uppercase tracking-wider ${
                  enabled()
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                    : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                }`}
              >
                {enabled()
                  ? t("timeline.commandSandboxStateOn")
                  : t("timeline.commandSandboxStateOff")}
              </span>
            </div>
            <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
              {error() || description()}
            </p>
          </div>
        </button>
      </div>
    </Show>
  );
}
