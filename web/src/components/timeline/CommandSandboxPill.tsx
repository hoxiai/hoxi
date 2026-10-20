// web/src/components/timeline/CommandSandboxPill.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { LoaderCircle, ShieldCheck, ShieldOff } from "lucide-solid";
import { t } from "../../i18n";
import {
  fetchSandboxSession,
  updateSandboxSession,
  type SandboxSessionState,
} from "../../services/sandbox-api";

export default function CommandSandboxPill(props: {
  sessionID?: string | null;
  directory?: string | null;
  busy: boolean;
}) {
  const [state, setState] = createSignal<SandboxSessionState | null>(null);
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  createEffect(() => {
    const id = props.sessionID;
    setError(null);
    if (!id) {
      setState(null);
      return;
    }
    let stale = false;
    fetchSandboxSession(id)
      .then((next) => {
        if (!stale) setState(next);
      })
      .catch(() => {
        if (!stale) setState(null);
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

  const title = createMemo(() => {
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
      if (props.sessionID === id) setState(next);
    } catch (err) {
      if (props.sessionID === id) setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Show when={props.sessionID}>
      <button
        type="button"
        onClick={() => void toggle()}
        disabled={disabled()}
        title={error() || title()}
        class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed ${
          enabled()
            ? "text-[var(--accent-primary)] bg-[var(--accent-surface)]"
            : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
      >
        <Show when={!saving()} fallback={<LoaderCircle size={13} class="animate-spin shrink-0" />}>
          <Show
            when={enabled()}
            fallback={<ShieldOff size={13} class="shrink-0 text-[var(--text-muted)]" />}
          >
            <ShieldCheck size={13} class="shrink-0" />
          </Show>
        </Show>
        <span>
          {enabled()
            ? t("timeline.commandSandboxOn")
            : t("timeline.commandSandboxOff")}
        </span>
      </button>
    </Show>
  );
}
