// web/src/components/models/ProviderCatalogTab.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  Ban,
  ChevronDown,
  ChevronRight,
  Eye,
  KeyRound,
  Pencil,
  Plus,
  RotateCcw,
  Server,
  Trash2,
} from "lucide-solid";
import type { ProviderInfo } from "../../services/api";
import {
  disconnectProvider,
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
  openProviderModal,
  POPULAR_PROVIDER_IDS,
  toggleDisabledProvider,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { ProviderConnectView, ProviderSelectAllView } from "./ProviderConnectSubView";
import { ProviderHealthBadge } from "./ProviderQualityCard";
import { isModelFree } from "./model-selector-utils";
import LocalCliBridgeBanner from "./LocalCliBridgeBanner";

type SubView = "list" | "connect" | "select-all";

export default function ProviderCatalogTab() {
  const [subView, setSubView] = createSignal<SubView>("list");
  const [targetID, setTargetID] = createSignal<string>("deepseek");
  const [disabledExpanded, setDisabledExpanded] = createSignal(false);
  const [busyID, setBusyID] = createSignal<string | null>(null);

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));
  const disabledSet = createMemo(() => {
    const arr = workspace.globalConfig?.disabled_providers;
    return new Set<string>(Array.isArray(arr) ? arr : []);
  });

  const providerMap = createMemo(() => {
    const map = new Map<string, ProviderInfo>();
    for (const p of workspace.providers) {
      map.set(p.id, p);
    }
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    for (const [id, cfg] of Object.entries<any>(cfgProviders)) {
      if (!cfg || typeof cfg !== "object") continue;
      if (!map.has(id)) {
        map.set(id, {
          id,
          name: cfg.name || id,
          source: "custom",
          npm: cfg.npm,
          options: cfg.options,
          models: cfg.models || {},
        });
      }
    }
    return map;
  });

  const connectedProviders = createMemo(() => {
    const conn = connectedSet();
    const dis = disabledSet();
    const cfgProviders = workspace.globalConfig?.provider ?? {};
    const result: ProviderInfo[] = [];

    for (const p of providerMap().values()) {
      if (dis.has(p.id)) continue;
      const isConn = conn.has(p.id) || Boolean(cfgProviders[p.id]);
      if (isConn) {
        result.push(p);
      }
    }
    return result.sort((a, b) => {
      const aPrio = a.source === "runner" || isCustomOrConfiguredProvider(a);
      const bPrio = b.source === "runner" || isCustomOrConfiguredProvider(b);
      if (aPrio !== bPrio) return aPrio ? -1 : 1;
      return (a.name || a.id).localeCompare(b.name || b.id);
    });
  });

  const popularNotConnected = createMemo(() => {
    const connIds = new Set(connectedProviders().map((p) => p.id));
    const dis = disabledSet();
    const map = providerMap();
    const out: ProviderInfo[] = [];

    for (const pid of POPULAR_PROVIDER_IDS) {
      if (connIds.has(pid) || dis.has(pid)) continue;
      const found = map.get(pid);
      if (found) {
        out.push(found);
      } else {
        out.push({ id: pid, name: pid, models: {} });
      }
    }
    return out;
  });

  const disabledProviders = createMemo(() => {
    const dis = disabledSet();
    const map = providerMap();
    return Array.from(dis).map((id) => map.get(id) || { id, name: id, models: {} });
  });

  const targetProvider = createMemo<ProviderInfo>(() => {
    const id = targetID();
    return providerMap().get(id) || { id, name: id, models: {} };
  });

  const openConnectView = (providerID: string) => {
    setTargetID(providerID);
    setSubView("connect");
  };

  const sourceLabel = (p: ProviderInfo) => {
    if (p.source === "runner") {
      if (p.id === "antigravity") return t("timeline.runtimeAntigravityLocal");
      if (p.id === "codebuddy") return t("timeline.runtimeCodeBuddyLocal");
      if (p.id === "qoder") return t("timeline.runtimeQoderLocal");
      return t("timeline.runtimeClaudeCodeLocal");
    }
    if (isHoxiCustomProvider(p.id)) return t("models.sourceHoxi");
    if (isImportedOpenCodeProvider(p.id)) return t("models.sourceOpenCode");
    if (isCustomOrConfiguredProvider(p)) return t("models.sourceCustom");
    if (p.source === "env") return t("models.sourceEnv");
    if (p.source === "config") return t("models.sourceConfig");
    return t("models.sourceApi");
  };

  const onDisconnect = async (pid: string) => {
    if (busyID()) return;
    setBusyID(pid);
    try {
      await disconnectProvider(pid);
    } finally {
      setBusyID(null);
    }
  };

  const onToggleDisabled = async (pid: string, disabled: boolean) => {
    if (busyID()) return;
    setBusyID(pid);
    try {
      await toggleDisabledProvider(pid, disabled);
    } finally {
      setBusyID(null);
    }
  };

  return (
    <div class="flex flex-1 flex-col min-h-0 overflow-hidden">
      <Show when={subView() === "list"}>
        <div class="flex-1 overflow-y-auto p-5 space-y-6">
          <LocalCliBridgeBanner />

          {/* Custom Provider Quick Card */}
          <div class="flex items-center justify-between gap-4 rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/40 px-4 py-3.5">
            <div class="flex items-center gap-3 min-w-0">
              <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <Server size={17} />
              </div>
              <div class="min-w-0">
                <div class="text-[13px] font-semibold text-[var(--text-primary)]">
                  {t("models.customProviderCardTitle")}
                </div>
                <div class="text-[11.5px] text-[var(--text-secondary)] line-clamp-1">
                  {t("models.customProviderCardDesc")}
                </div>
              </div>
            </div>
            <button
              type="button"
              onClick={() => openProviderModal("custom", null)}
              class="shrink-0 inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
            >
              <Plus size={13} />
              {t("models.configureCustom")}
            </button>
          </div>

          {/* Connected Providers Section */}
          <div class="space-y-2.5">
            <div class="flex items-center justify-between px-1">
              <h3 class="text-[11.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                {t("models.connectedSection")} ({connectedProviders().length})
              </h3>
            </div>

            <Show
              when={connectedProviders().length > 0}
              fallback={
                <div class="rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-elevated)]/30 p-5 text-center text-[12px] text-[var(--text-muted)]">
                  {t("models.noConnectedProviders")}
                </div>
              }
            >
              <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 overflow-hidden">
                <For each={connectedProviders()}>
                  {(item) => {
                    const isCustom = () => isCustomOrConfiguredProvider(item);
                    const isImported = () => isImportedOpenCodeProvider(item.id);
                    const modelCount = () => Object.keys(item.models || {}).length;
                    const freeCount = () =>
                      Object.entries(item.models || {}).filter(([mID, mInfo]) =>
                        isModelFree(item.id, mID, mInfo)
                      ).length;
                    const customUrl = () =>
                      workspace.hoxiProviders?.[item.id]?.options?.baseURL ||
                      workspace.globalConfig?.provider?.[item.id]?.options?.baseURL ||
                      item.options?.baseURL;

                    return (
                      <div class="flex items-center justify-between gap-3 px-4 py-3 hover:bg-[var(--bg-hover)]/50 transition-colors">
                        <div class="flex items-center gap-3 min-w-0">
                          <ProviderIcon id={item.id} class="w-5 h-5 shrink-0" />
                          <div class="min-w-0">
                            <div class="flex items-center gap-2 flex-wrap">
                              <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
                                {item.name || item.id}
                              </span>
                              <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                                {item.id}
                              </span>
                              <span
                                class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${
                                  isImported()
                                    ? "bg-amber-500/10 text-amber-400"
                                    : "bg-[var(--ok-surface)] text-[var(--ok-text)]"
                                }`}
                              >
                                <span
                                  class={`h-1.5 w-1.5 rounded-full ${
                                    isImported() ? "bg-amber-400" : "bg-[var(--ok)]"
                                  }`}
                                />
                                {sourceLabel(item)}
                              </span>
                              <ProviderHealthBadge providerID={item.id} />
                              <span class="text-[11px] text-[var(--text-faint)]">
                                {t("models.modelsCount", { count: modelCount() })}
                              </span>
                              <Show when={freeCount() > 0}>
                                <span class="rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[10px] font-semibold text-[var(--ok-text)]">
                                  {t("models.freeModelsCount", { count: freeCount() })}
                                </span>
                              </Show>
                            </div>
                            <Show when={customUrl()}>
                              <div class="mt-0.5 font-mono text-[10.5px] text-[var(--text-faint)] truncate">
                                {customUrl()}
                              </div>
                            </Show>
                          </div>
                        </div>

                        <div class="flex items-center gap-1.5 shrink-0">
                          <Show when={isCustom()}>
                            <button
                              type="button"
                              onClick={() => openProviderModal("custom", item.id)}
                              class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:border-[var(--border-strong)] transition-colors cursor-pointer"
                              title={
                                isImported()
                                  ? t("models.viewImportedEndpoint")
                                  : t("models.editProvider")
                              }
                            >
                              <Show when={isImported()} fallback={<Pencil size={11} />}>
                                <Eye size={11} />
                              </Show>
                              {isImported()
                                ? t("models.viewImportedEndpoint")
                                : t("models.editProvider")}
                            </button>
                          </Show>
                          <Show when={!isImported() && item.source !== "runner"}>
                            <button
                              type="button"
                              onClick={() => openConnectView(item.id)}
                              class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:border-[var(--border-strong)] transition-colors cursor-pointer"
                              title={t("models.changeKey")}
                            >
                              <KeyRound size={11} />
                              {t("models.changeKey")}
                            </button>
                          </Show>
                          <button
                            type="button"
                            disabled={busyID() === item.id}
                            onClick={() => void onToggleDisabled(item.id, true)}
                            class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-secondary)] transition-colors cursor-pointer"
                            title={t("models.disableProvider")}
                          >
                            <Ban size={13} />
                          </button>
                          <Show when={!isImported() && item.source !== "runner"}>
                            <button
                              type="button"
                              disabled={busyID() === item.id}
                              onClick={() => void onDisconnect(item.id)}
                              class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] transition-colors cursor-pointer"
                              title={t("models.disconnect")}
                            >
                              <Trash2 size={13} />
                            </button>
                          </Show>
                        </div>
                      </div>
                    );
                  }}
                </For>
              </div>
            </Show>
          </div>

          {/* Popular Providers Section */}
          <div class="space-y-2.5">
            <div class="flex items-center justify-between px-1">
              <h3 class="text-[11.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                {t("models.popularSection")}
              </h3>
              <button
                type="button"
                onClick={() => setSubView("select-all")}
                class="text-[11.5px] font-medium text-[var(--accent-primary)] hover:underline cursor-pointer"
              >
                {t("models.viewAllProviders")} ({providerMap().size}) →
              </button>
            </div>

            <div class="grid grid-cols-2 gap-2.5">
              <For each={popularNotConnected()}>
                {(item) => {
                  const modelCount = () => Object.keys(item.models || {}).length;
                  return (
                    <div class="flex items-center justify-between gap-2.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-3.5 py-2.5 hover:border-[var(--border-strong)] transition-colors">
                      <div class="flex items-center gap-2.5 min-w-0">
                        <ProviderIcon id={item.id} class="w-4 h-4 shrink-0" />
                        <div class="min-w-0">
                          <div class="text-[12.5px] font-medium text-[var(--text-primary)] truncate">
                            {item.name || item.id}
                          </div>
                          <div class="text-[10.5px] text-[var(--text-faint)]">
                            {modelCount() > 0
                              ? t("models.modelsCount", { count: modelCount() })
                              : item.id}
                          </div>
                        </div>
                      </div>
                      <div class="flex items-center gap-1 shrink-0">
                        <button
                          type="button"
                          onClick={() => openConnectView(item.id)}
                          class="rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--accent-surface)] hover:text-[var(--accent-primary)] hover:border-[var(--accent-border)] transition-colors cursor-pointer"
                        >
                          {t("models.connect")}
                        </button>
                        <button
                          type="button"
                          onClick={() => void onToggleDisabled(item.id, true)}
                          class="rounded-lg p-1 text-[var(--text-faint)] hover:text-[var(--text-secondary)] cursor-pointer"
                          title={t("models.disableProvider")}
                        >
                          <Ban size={12} />
                        </button>
                      </div>
                    </div>
                  );
                }}
              </For>
            </div>
          </div>

          {/* Disabled Providers Collapsible */}
          <Show when={disabledProviders().length > 0}>
            <div class="pt-1">
              <button
                type="button"
                onClick={() => setDisabledExpanded(!disabledExpanded())}
                class="flex items-center gap-1.5 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Show when={disabledExpanded()} fallback={<ChevronRight size={13} />}>
                  <ChevronDown size={13} />
                </Show>
                {t("models.disabledSection")} ({disabledProviders().length})
              </button>

              <Show when={disabledExpanded()}>
                <div class="mt-2 grid grid-cols-2 gap-2">
                  <For each={disabledProviders()}>
                    {(item) => (
                      <div class="flex items-center justify-between rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 px-3 py-2 opacity-75">
                        <div class="flex items-center gap-2 min-w-0">
                          <ProviderIcon id={item.id} class="w-4 h-4 shrink-0" />
                          <span class="truncate text-[12px] text-[var(--text-secondary)]">
                            {item.name || item.id}
                          </span>
                        </div>
                        <button
                          type="button"
                          onClick={() => void onToggleDisabled(item.id, false)}
                          class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                        >
                          <RotateCcw size={11} />
                          {t("models.enableProvider")}
                        </button>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>
        </div>
      </Show>

      <Show when={subView() === "connect"}>
        <ProviderConnectView
          provider={targetProvider()}
          connected={connectedSet().has(targetID())}
          onBack={() => setSubView("list")}
        />
      </Show>

      <Show when={subView() === "select-all"}>
        <ProviderSelectAllView
          providers={Array.from(providerMap().values())}
          connectedSet={connectedSet()}
          disabledSet={disabledSet()}
          onSelect={(pid) => openConnectView(pid)}
          onBack={() => setSubView("list")}
        />
      </Show>
    </div>
  );
}
