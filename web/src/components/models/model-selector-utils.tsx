// web/src/components/models/model-selector-utils.tsx
import { Show } from "solid-js";
import { Image, Pencil, Sparkles, Wrench } from "lucide-solid";
import type { ProviderInfo } from "../../services/api";
import { isCustomOrConfiguredProvider } from "../../store/workspace";
import { t } from "../../i18n";

export interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  isCustom: boolean;
  reasoning: boolean;
  image: boolean;
  toolcall: boolean;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  free: boolean;
}

export function formatTokenLimit(n?: number): string | null {
  if (!n || n <= 0) return null;
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function formatPricePerMillion(cost?: number): string | null {
  if (cost === undefined || cost === null) return null;
  if (cost === 0) return "$0";
  // OpenCode cost is either per-1M tokens or per-token depending on catalog; normalize cleanly
  const val = cost < 0.001 ? cost * 1_000_000 : cost;
  return `$${val < 1 ? val.toFixed(2) : val.toFixed(2).replace(/\.00$/, "")}/M`;
}

export function extractFlattenedModels(
  providers: ProviderInfo[],
  connectedIds: Set<string>,
  globalConfig: Record<string, any>
): FlattenedModel[] {
  const disabledSet = new Set<string>(
    Array.isArray(globalConfig?.disabled_providers) ? globalConfig.disabled_providers : []
  );
  const cfgProviders = globalConfig?.provider ?? {};
  const out: FlattenedModel[] = [];

  for (const p of providers) {
    if (disabledSet.has(p.id)) continue;
    const isCustom = isCustomOrConfiguredProvider(p);
    const isConn = connectedIds.has(p.id) || Boolean(cfgProviders[p.id]);

    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const cfgModel = cfgProviders?.[p.id]?.models?.[mID];
      const reasoning = Boolean(
        mInfo.capabilities?.reasoning ??
          mInfo.reasoning ??
          cfgModel?.reasoning
      );
      const image = Boolean(
        mInfo.capabilities?.input?.image ||
          (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) ||
          (Array.isArray(cfgModel?.modalities?.input) &&
            cfgModel.modalities.input.includes("image"))
      );
      const toolcall = Boolean(mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true);
      const inCost = mInfo.cost?.input;
      const outCost = mInfo.cost?.output;
      const free =
        mID.endsWith("-free") ||
        (inCost === 0 && outCost === 0);

      out.push({
        providerID: p.id,
        providerName: p.name || p.id,
        modelID: mID,
        modelName: mInfo.name || cfgModel?.name || mID,
        connected: isConn,
        isCustom,
        reasoning,
        image,
        toolcall,
        contextLimit: mInfo.limit?.context,
        outputLimit: mInfo.limit?.output,
        inputCost: inCost,
        outputCost: outCost,
        free,
      });
    }
  }
  return out;
}

/**
 * Kilo's token-scored model search ranker (`rankModelSearch`).
 */
export function rankModelSearch(models: FlattenedModel[], rawQuery: string): FlattenedModel[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: FlattenedModel; score: number }> = [];
  for (const m of models) {
    const idLower = m.modelID.toLowerCase();
    const nameLower = m.modelName.toLowerCase();
    const provLower = `${m.providerName} ${m.providerID}`.toLowerCase();
    const haystack = `${idLower} ${nameLower} ${provLower}`;

    if (!tokens.every((tok) => haystack.includes(tok))) continue;

    let score = 0;
    if (idLower === q || nameLower === q) score += 200;
    else if (idLower.startsWith(q) || nameLower.startsWith(q)) score += 120;
    else if (idLower.includes(q) || nameLower.includes(q)) score += 70;
    if (m.connected) score += 40;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function ModelPreviewFooter(props: {
  model: FlattenedModel;
  onEditCustom: (providerID: string) => void;
}) {
  const ctxText = () => formatTokenLimit(props.model.contextLimit);
  const outText = () => formatTokenLimit(props.model.outputLimit);
  const inPrice = () => formatPricePerMillion(props.model.inputCost);
  const outPrice = () => formatPricePerMillion(props.model.outputCost);

  return (
    <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 px-3 py-2 text-[11px] space-y-1">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0 flex items-center gap-1.5">
          <span class="font-semibold text-[var(--text-primary)] truncate">
            {props.model.modelName}
          </span>
          <span class="font-mono text-[10px] text-[var(--text-faint)] truncate">
            ({props.model.providerID}/{props.model.modelID})
          </span>
        </div>
        <Show when={props.model.isCustom}>
          <button
            type="button"
            onClick={() => props.onEditCustom(props.model.providerID)}
            class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:border-[var(--accent-border)] cursor-pointer"
          >
            <Pencil size={10} />
            {t("models.editProvider")}
          </button>
        </Show>
      </div>

      <div class="flex items-center justify-between gap-2 text-[10.5px] text-[var(--text-muted)]">
        <div class="flex items-center gap-2 flex-wrap">
          <Show when={ctxText()}>
            <span>
              {t("models.contextWindow")}: <strong class="font-mono">{ctxText()}</strong>
            </span>
          </Show>
          <Show when={outText()}>
            <span>
              {t("models.maxOutput")}: <strong class="font-mono">{outText()}</strong>
            </span>
          </Show>
          <Show
            when={!props.model.free && (inPrice() || outPrice())}
            fallback={
              <Show when={props.model.free}>
                <span class="rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[10px] font-medium text-[var(--ok-text)]">
                  {t("models.freeModel")}
                </span>
              </Show>
            }
          >
            <span class="font-mono">
              {inPrice() || "-"} in / {outPrice() || "-"} out
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-1 shrink-0">
          <Show when={props.model.reasoning}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--accent-surface)] px-1.5 py-[1px] text-[9.5px] font-medium text-[var(--accent-primary)]">
              <Sparkles size={9} />
              {t("models.reasoningCap")}
            </span>
          </Show>
          <Show when={props.model.image}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-[1px] text-[9.5px] font-medium text-[var(--text-secondary)]">
              <Image size={9} />
              {t("models.imageCap")}
            </span>
          </Show>
          <Show when={props.model.toolcall}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-[1px] text-[9.5px] text-[var(--text-muted)]">
              <Wrench size={9} />
              Tools
            </span>
          </Show>
        </div>
      </div>
    </div>
  );
}
