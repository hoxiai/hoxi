import { createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import { Icon } from "../icons";

/**
 * Warning shown while a busy session has been flagged by the runner stall
 * watchdog (`session.stall`). Independent of the thinking placeholder, so it
 * also appears while a silent tool call is running.
 */
export default function StalledNotice(props: { stalledSince?: number }) {
  const [now, setNow] = createSignal(Date.now());
  const timer = setInterval(() => setNow(Date.now()), 10_000);
  onCleanup(() => clearInterval(timer));

  const minutes = () => {
    const since = props.stalledSince || 0;
    return since > 0 ? Math.max(1, Math.floor((now() - since) / 60_000)) : 0;
  };

  return (
    <Show when={minutes() > 0}>
      <div
        data-component="execution-stalled"
        class="mt-3 flex items-start gap-2 rounded-lg border border-[var(--warn-border)] bg-[var(--warn-surface)] px-3 py-2 text-[12px] leading-relaxed text-[var(--warn-text)]"
      >
        <Icon name="warning" size="small" class="mt-0.5 shrink-0" />
        <span>{t("timeline.stalledHint", { minutes: minutes() })}</span>
      </div>
    </Show>
  );
}
