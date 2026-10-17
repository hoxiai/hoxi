// web/src/components/models/model-selector-utils.tsx
import { For, Show } from "solid-js";
import {
  Clock,
  Image,
  KeyRound,
  Pencil,
  Plus,
  Sparkles,
  Star,
  Wrench,
} from "lucide-solid";
import type { ProviderInfo } from "../../services/api";
import {
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
  openProviderModal,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { ProviderHealthBadge } from "./ProviderQualityCard";

export interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  isCustom: boolean;
  source?: string;
  baseURL?: string;
  reasoning: boolean;
  image: boolean;
  toolcall: boolean;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  free: boolean;
}

export interface ModelSourceInfo {
  label: string;
  configHint?: string;
  isImported: boolean;
}

export function getModelSourceInfo(model: FlattenedModel): ModelSourceInfo {
  const pid = model.providerID;
  if (isHoxiCustomProvider(pid)) {
    return {
      label: t("models.sourceHoxi"),
      configHint: ".hoxi/models.json",
      isImported: false,
    };
  }
  if (isImportedOpenCodeProvider(pid)) {
    return {
      label: t("models.sourceOpenCode"),
      configHint: "opencode",
      isImported: true,
    };
  }
  if (model.isCustom) {
    return {
      label: t("models.sourceCustom"),
      isImported: false,
    };
  }
  if (pid === "opencode" && model.free && !workspace.connectedProviders.includes("opencode")) {
    return {
      label: t("models.sourceBuiltInFree"),
      isImported: false,
    };
  }
  if (model.source === "env") {
    return { label: t("models.sourceEnv"), isImported: false };
  }
  if (model.source === "config") {
    return { label: t("models.sourceConfig"), isImported: false };
  }
  return { label: t("models.sourceApi"), isImported: false };
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
  const val = cost < 0.001 ? cost * 1_000_000 : cost;
  return `$${val < 1 ? val.toFixed(2) : val.toFixed(2).replace(/\.00$/, "")}/M`;
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (providerID !== "opencode") return false;
  const inCost = mInfo?.cost?.input;
  return !mInfo?.cost || inCost === 0 || modelID.endsWith("-free");
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
    const baseConn = connectedIds.has(p.id) || Boolean(cfgProviders[p.id]);
    const baseURL =
      workspace.hoxiProviders?.[p.id]?.options?.baseURL ||
      cfgProviders?.[p.id]?.options?.baseURL ||
      p.options?.baseURL;

    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const cfgModel = cfgProviders?.[p.id]?.models?.[mID];
      const reasoning = Boolean(
        mInfo.capabilities?.reasoning ?? mInfo.reasoning ?? cfgModel?.reasoning
      );
      const image = Boolean(
        mInfo.capabilities?.input?.image ||
          (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) ||
          (Array.isArray(cfgModel?.modalities?.input) &&
            cfgModel.modalities.input.includes("image"))
      );
      const toolcall = Boolean(mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true);
      const inCost = mInfo.cost?.input ?? cfgModel?.cost?.input;
      const outCost = mInfo.cost?.output ?? cfgModel?.cost?.output;
      const free = isModelFree(p.id, mID, mInfo);
      const isConn = baseConn || (p.id === "opencode" && free);

      out.push({
        providerID: p.id,
        providerName: p.name || p.id,
        modelID: mID,
        modelName: mInfo.name || cfgModel?.name || mID,
        connected: isConn,
        isCustom,
        source: p.source,
        baseURL,
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

export function rankModelSearch(models: FlattenedModel[], rawQuery: string): FlattenedModel[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: FlattenedModel; score: number }> = [];
  for (const m of models) {
    const idLower = m.modelID.toLowerCase();
    const nameLower = m.modelName.toLowerCase();
    const provLower = `${m.providerName} ${m.providerID}`.toLowerCase();
    const freeTag = m.free ? `free ${t("models.freeModel").toLowerCase()}` : "";
    const haystack = `${idLower} ${nameLower} ${provLower} ${freeTag}`;

    if (!tokens.every((tok) => haystack.includes(tok))) continue;

    let score = 0;
    if (idLower === q || nameLower === q) score += 200;
    else if (idLower.startsWith(q) || nameLower.startsWith(q)) score += 120;
    else if (idLower.includes(q) || nameLower.includes(q)) score += 70;
    if (m.connected) score += 40;
    if (m.free) score += 25;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function ModelSelectorRail(props: {
  rail: string;
  hasQuery: boolean;
  connectedTotal: number;
  providers: Array<{ id: string; name: string; count: number; isCustom: boolean }>;
  onSelectRail: (rail: string) => void;
  onClosePopover: () => void;
}) {
  return (
    <div class="w-[172px] shrink-0 border-r border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 flex flex-col justify-between p-1.5">
      <div class="overflow-y-auto space-y-0.5 pr-0.5">
        <button
          type="button"
          onClick={() => props.onSelectRail("connected")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "connected" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)] shrink-0" />
            <span class="truncate">{t("models.allConnected")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{props.connectedTotal}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("favorites")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "favorites" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Star size={12} class="shrink-0 text-amber-500" />
            <span class="truncate">{t("models.favorites")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{workspace.favoriteModels.length}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("recent")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "recent" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Clock size={12} class="shrink-0 text-[var(--text-muted)]" />
            <span class="truncate">{t("models.recent")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{workspace.recentModels.length}</span>
        </button>

        <div class="my-1 border-t border-[var(--border-subtle)]" />

        <For each={props.providers}>
          {(prov) => (
            <div
              class={`group flex items-center justify-between gap-1 rounded-lg px-2 py-1.5 text-[11.5px] transition-colors cursor-pointer ${
                props.rail === prov.id && !props.hasQuery
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
              onClick={() => props.onSelectRail(prov.id)}
            >
              <span class="flex items-center gap-1.5 min-w-0 truncate">
                <ProviderIcon id={prov.id} class="w-3.5 h-3.5 shrink-0" />
                <span class="truncate">{prov.name}</span>
              </span>
              <span class="flex items-center gap-1 shrink-0">
                <Show when={prov.isCustom}>
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      props.onClosePopover();
                      openProviderModal("custom", prov.id);
                    }}
                    class="opacity-0 group-hover:opacity-100 rounded p-0.5 hover:bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--accent-primary)] cursor-pointer"
                    title={t("models.editProvider")}
                  >
                    <Pencil size={10} />
                  </button>
                </Show>
                <span class="font-mono text-[10px] opacity-65">{prov.count}</span>
              </span>
            </div>
          )}
        </For>
      </div>

      <div class="mt-1 space-y-1 pt-1 border-t border-[var(--border-subtle)]">
        <button
          type="button"
          onClick={() => {
            props.onClosePopover();
            openProviderModal("custom", null);
          }}
          class="w-full inline-flex items-center justify-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Plus size={11} />
          {t("models.tabCustom")}
        </button>
        <button
          type="button"
          onClick={() => {
            props.onClosePopover();
            openProviderModal("providers", null);
          }}
          class="w-full inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-1.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
        >
          <KeyRound size={11} />
          {t("models.manageKeys")}
        </button>
      </div>
    </div>
  );
}

export function ModelPreviewFooter(props: {
  model: FlattenedModel;
  onEditCustom: (providerID: string) => void;
}) {
  const ctxText = () => formatTokenLimit(props.model.contextLimit);
  const outText = () => formatTokenLimit(props.model.outputLimit);
  const inPrice = () => formatPricePerMillion(props.model.inputCost);
  const outPrice = () => formatPricePerMillion(props.model.outputCost);
  const srcInfo = () => getModelSourceInfo(props.model);

  return (
    <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 px-3 py-2 text-[11px] space-y-1">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0 flex items-center gap-1.5">
          <span class="font-semibold text-[var(--text-primary)] truncate">
            {props.model.modelName}
          </span>
          <span
            class={`shrink-0 inline-flex items-center gap-1 rounded-full px-1.5 py-[1px] text-[9.5px] font-medium ${
              srcInfo().isImported
                ? "bg-amber-500/10 text-amber-400"
                : "bg-[var(--ok-surface)] text-[var(--ok-text)]"
            }`}
          >
            <span
              class={`h-1.5 w-1.5 rounded-full ${
                srcInfo().isImported ? "bg-amber-400" : "bg-[var(--ok)]"
              }`}
            />
            {srcInfo().label}
          </span>
          <ProviderHealthBadge
            providerID={props.model.providerID}
            modelID={props.model.modelID}
          />
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
              {t("models.toolsCap")}
            </span>
          </Show>
        </div>
      </div>
    </div>
  );
}


