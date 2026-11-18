// web/src/components/approval/plan-consent-preview.tsx
import { For, Show } from "solid-js";
import type { PlanConsentPreview } from "../../services/sse-types";
import { t } from "../../i18n";

export function extractPlanPreview(raw: unknown): PlanConsentPreview | null {
  if (!raw || typeof raw !== "object") return null;
  const obj = raw as Record<string, unknown>;
  const steps = Array.isArray(obj.steps)
    ? obj.steps.map((s) => String(s ?? "").trim()).filter(Boolean)
    : [];
  const files = Array.isArray(obj.files)
    ? obj.files.map((s) => String(s ?? "").trim()).filter(Boolean)
    : [];
  const risks = Array.isArray(obj.risks)
    ? obj.risks.map((s) => String(s ?? "").trim()).filter(Boolean)
    : [];
  const title = typeof obj.title === "string" ? obj.title.trim() : "";
  const summary = typeof obj.summary === "string" ? obj.summary.trim() : "";
  if (!title && steps.length === 0) return null;
  return { title, summary, steps, files, risks };
}

export function PlanConsentPreviewCard(props: {
  plan: PlanConsentPreview;
  promotedToBuild?: boolean;
}) {
  return (
    <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/75 px-3 py-2.5 space-y-2 text-[12px]">
      <div class="flex items-center justify-between gap-2">
        <span class="font-semibold text-[var(--text-primary)]">
          {props.plan.title || t("approval.planConsentTitle")}
        </span>
        <Show when={props.promotedToBuild}>
          <span class="rounded-md bg-[var(--ok-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--ok-text)]">
            {t("toolCard.planPromotedBadge")}
          </span>
        </Show>
      </div>

      <Show when={props.plan.summary}>
        <p class="text-[12px] text-[var(--text-secondary)] leading-snug">
          {props.plan.summary}
        </p>
      </Show>

      <Show when={(props.plan.steps?.length ?? 0) > 0}>
        <div class="space-y-1">
          <div class="text-[11px] font-medium text-[var(--text-muted)]">
            {t("approval.planConsentStepsLabel")}
          </div>
          <ol class="space-y-1 pl-4 list-decimal text-[var(--text-primary)]">
            <For each={props.plan.steps}>
              {(step) => <li class="leading-snug">{step}</li>}
            </For>
          </ol>
        </div>
      </Show>

      <Show when={(props.plan.files?.length ?? 0) > 0}>
        <div class="space-y-1">
          <div class="text-[11px] font-medium text-[var(--text-muted)]">
            {t("approval.planConsentFilesLabel")}
          </div>
          <div class="flex flex-wrap gap-1">
            <For each={props.plan.files}>
              {(file) => (
                <span class="rounded border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-secondary)]">
                  {file}
                </span>
              )}
            </For>
          </div>
        </div>
      </Show>

      <Show when={(props.plan.risks?.length ?? 0) > 0}>
        <div class="space-y-1">
          <div class="text-[11px] font-medium text-[var(--warn-text)]">
            {t("approval.planConsentRisksLabel")}
          </div>
          <ul class="space-y-0.5 pl-4 list-disc text-[11px] text-[var(--text-secondary)]">
            <For each={props.plan.risks}>
              {(risk) => <li class="leading-snug">{risk}</li>}
            </For>
          </ul>
        </div>
      </Show>
    </div>
  );
}
