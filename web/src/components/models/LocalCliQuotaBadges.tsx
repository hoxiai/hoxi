// web/src/components/models/LocalCliQuotaBadges.tsx
import { createMemo, Show } from "solid-js";
import type { LocalCLIQuotaSnapshot, LocalCLIQuotaWindow } from "../../services/api";
import {
  formatCompactTokenCount,
  formatQuotaResetTime,
  getCandidateBreaker,
  providerBreakers,
} from "../../store/workspace";
import { t } from "../../i18n";

function quotaToneClass(remPercent: number | undefined): string {
  const rem = Number(remPercent ?? 100);
  if (rem <= 10) {
    return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
  }
  if (rem < 30) {
    return "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]";
  }
  return "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]";
}

function windowResetTooltip(w: LocalCLIQuotaWindow | undefined): string | undefined {
  const label = formatQuotaResetTime(w?.resetsAt);
  return label ? t("models.cliQuotaWindowResetsAt", { time: label }) : undefined;
}

export function LocalCliQuotaBadges(props: {
  quota?: LocalCLIQuotaSnapshot;
  providerID?: string;
  modelID?: string;
  detailed?: boolean;
}) {
  const activeCooldownReset = createMemo(() => {
    const pid = (props.providerID || "").trim();
    if (!pid) return "";
    const mid = (props.modelID || "").trim();
    if (mid) {
      const snap = getCandidateBreaker(pid, mid);
      if (
        snap?.open &&
        snap.openUntil &&
        snap.openUntil > Date.now() &&
        (snap.lastClass === "quota" || snap.lastClass === "rate_limit")
      ) {
        return formatQuotaResetTime(snap.openUntil);
      }
      return "";
    }
    const all = providerBreakers();
    const now = Date.now();
    let earliest = 0;
    for (const snap of Object.values(all)) {
      if (
        snap?.providerID === pid &&
        snap.open &&
        snap.openUntil &&
        snap.openUntil > now &&
        (snap.lastClass === "quota" || snap.lastClass === "rate_limit")
      ) {
        if (earliest === 0 || snap.openUntil < earliest) {
          earliest = snap.openUntil;
        }
      }
    }
    return earliest > 0 ? formatQuotaResetTime(earliest) : "";
  });

  const hasPrimary = () => typeof props.quota?.primary?.remainingPercent === "number";
  const hasSecondary = () => typeof props.quota?.secondary?.remainingPercent === "number";
  const hasWeeklyUsage = () =>
    (props.quota?.weeklyTokens ?? 0) > 0 || (props.quota?.weeklyTurns ?? 0) > 0;
  const hasAny = () =>
    Boolean(props.quota?.planType) ||
    hasPrimary() ||
    hasSecondary() ||
    hasWeeklyUsage() ||
    Boolean(activeCooldownReset());

  return (
    <Show when={hasAny()}>
      <div class="flex flex-wrap items-center gap-1 text-[11px] leading-tight">
        <Show when={props.quota?.planType}>
          <span class="rounded border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-[1px] font-mono text-[10px] font-semibold uppercase text-[var(--text-secondary)]">
            {props.quota!.planType}
          </span>
        </Show>

        <Show when={hasPrimary()}>
          <span
            title={windowResetTooltip(props.quota?.primary)}
            class={`inline-flex items-center gap-1 rounded border px-1.5 py-[1px] font-mono text-[10px] font-medium ${quotaToneClass(
              props.quota?.primary?.remainingPercent
            )}`}
          >
            {t("models.cliQuotaWindow5h", {
              percent: Math.round(props.quota!.primary!.remainingPercent!),
            })}
            <Show when={props.detailed && formatQuotaResetTime(props.quota?.primary?.resetsAt)}>
              <span class="opacity-80">
                · {t("models.cliQuotaWindowResetsAt", { time: formatQuotaResetTime(props.quota?.primary?.resetsAt) })}
              </span>
            </Show>
          </span>
        </Show>

        <Show when={hasSecondary()}>
          <span
            title={windowResetTooltip(props.quota?.secondary)}
            class={`inline-flex items-center gap-1 rounded border px-1.5 py-[1px] font-mono text-[10px] font-medium ${quotaToneClass(
              props.quota?.secondary?.remainingPercent
            )}`}
          >
            {t("models.cliQuotaWindowWeekly", {
              percent: Math.round(props.quota!.secondary!.remainingPercent!),
            })}
            <Show when={props.detailed && formatQuotaResetTime(props.quota?.secondary?.resetsAt)}>
              <span class="opacity-80">
                · {t("models.cliQuotaWindowResetsAt", { time: formatQuotaResetTime(props.quota?.secondary?.resetsAt) })}
              </span>
            </Show>
          </span>
        </Show>

        <Show when={activeCooldownReset()}>
          <span class="inline-flex items-center gap-1 rounded border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-[1px] font-mono text-[10px] font-medium text-[var(--danger-text)]">
            {t("models.cliQuotaCooldownActive", { time: activeCooldownReset() })}
          </span>
        </Show>

        <Show when={hasWeeklyUsage() && (props.detailed || (!hasPrimary() && !hasSecondary()))}>
          <span class="font-mono text-[10px] text-[var(--text-muted)]">
            {(props.quota?.weeklyTokens ?? 0) > 0
              ? t("models.cliQuotaWeeklyUsage", {
                  tokens: formatCompactTokenCount(props.quota?.weeklyTokens),
                  turns: props.quota?.weeklyTurns ?? 0,
                })
              : t("models.cliQuotaWeeklyTurnsOnly", {
                  turns: props.quota?.weeklyTurns ?? 0,
                })}
          </span>
        </Show>
      </div>
    </Show>
  );
}
