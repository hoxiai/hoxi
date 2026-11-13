import { Show } from "solid-js";
import { Icon } from "../icons";
import { t } from "../../i18n";
import { TURN_BATCH_SIZE } from "./turn-window";

export function EarlierTurnsBanner(props: {
  hiddenMessageCount: number;
  totalCount: number;
  hasOlderHistory?: boolean;
  onLoadBatch: () => void;
  onLoadAll: () => void;
}) {
  const nextBatchCount = () => Math.min(props.hiddenMessageCount, TURN_BATCH_SIZE);
  return (
    <Show when={props.hiddenMessageCount > 0 || props.hasOlderHistory}>
      <div class="mb-5 flex flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 px-4 py-2.5 text-center select-none">
        <div class="text-[12px] text-[var(--text-faint)]">
          {props.hiddenMessageCount > 0
            ? t("timeline.hiddenTurnsHint", { count: props.hiddenMessageCount })
            : t("timeline.olderHistoryHint")}
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={props.onLoadBatch}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Icon name="arrow-up" size="small" class="text-[var(--accent-primary)]" />
            <span>
              {nextBatchCount() > 0
                ? t("timeline.loadEarlierTurns", { count: nextBatchCount() })
                : t("timeline.loadOlderHistory")}
            </span>
          </button>
          <Show when={props.hiddenMessageCount > TURN_BATCH_SIZE || props.hasOlderHistory}>
            <button
              type="button"
              onClick={props.onLoadAll}
              class="inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <span>
                {props.hasOlderHistory
                  ? t("timeline.loadFullHistory")
                  : t("timeline.loadAllEarlierTurns", { total: props.totalCount })}
              </span>
            </button>
          </Show>
        </div>
      </div>
    </Show>
  );
}
