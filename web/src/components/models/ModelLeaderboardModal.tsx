// web/src/components/models/ModelLeaderboardModal.tsx
import { For, onCleanup, onMount, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  Check,
  Coins,
  Cpu,
  ExternalLink,
  Flame,
  Globe,
  Radio,
  Sparkles,
  Trophy,
  X,
  Zap,
} from "lucide-solid";
import {
  closeLeaderboardModal,
  fetchGateways,
  leaderboardState,
  openExternalURL,
  resolveOfficialWebURL,
  selectModel,
  switchLeaderboardTab,
  workspace,
  type LeaderboardTab,
} from "../../store/workspace";
import { ProviderIcon, Spinner } from "../icons";
import { t } from "../../i18n";

export default function ModelLeaderboardModal() {
  onMount(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && leaderboardState.modalOpen) {
        closeLeaderboardModal();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => window.removeEventListener("keydown", handleKeyDown));
  });

  const isCurrentModel = (apiName?: string, slug?: string) => {
    const active = workspace.selectedModel?.modelID;
    if (!active) return false;
    return active === apiName || active === slug;
  };

  const handleApplyModel = (vendor: string, apiName?: string, slug?: string) => {
    const modelID = apiName || slug || "";
    if (!canApplyModel(vendor, modelID)) return;
    selectModel({ providerID: vendor, modelID });
  };

  const canApplyModel = (vendor: string, modelID: string) =>
    Boolean((workspace.connectedProviders.includes(vendor) || workspace.globalConfig?.provider?.[vendor]) &&
      workspace.providers.find((p) => p.id === vendor)?.models?.[modelID]);

  const handleOpenOfficialWeb = () => {
    openExternalURL(resolveOfficialWebURL("/models"));
  };

  return (
    <Presence when={leaderboardState.modalOpen}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4"
        onClick={() => closeLeaderboardModal()}
      >
        <div
          class="hoxi-modal-panel hoxi-elevation-4 relative w-full max-w-3xl max-h-[85vh] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] flex flex-col overflow-hidden"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div class="px-5 py-4 border-b border-[var(--border-subtle)] flex items-start justify-between gap-3 bg-[var(--bg-inset)]/40">
            <div class="flex items-start gap-3">
              <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-amber-500/20 to-orange-500/20 border border-amber-500/30 text-amber-500 shadow-xs">
                <Trophy size={20} />
              </div>
              <div>
                <div class="flex items-center gap-2">
                  <h2 class="text-[15px] font-bold text-[var(--text-primary)]">
                    {t("header.leaderboardModalTitle")}
                  </h2>
                  <span class="rounded-md bg-amber-500/10 border border-amber-500/25 px-1.5 py-0.5 text-[11px] font-semibold text-amber-500">
                    AA Benchmark
                  </span>
                </div>
                <p class="text-[12px] text-[var(--text-muted)] mt-0.5">
                  {t("header.leaderboardModalSubtitle")}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={() => closeLeaderboardModal()}
              class="p-1 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <X size={16} />
            </button>
          </div>

          {/* Navigation Tabs */}
          <div class="flex items-center justify-between px-5 pt-3 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]">
            <div class="flex items-center gap-1 overflow-x-auto pb-2.5">
              <button
                type="button"
                onClick={() => switchLeaderboardTab("overall")}
                class={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-medium transition-all cursor-pointer ${
                  leaderboardState.activeTab === "overall"
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
              >
                <Trophy size={13} class={leaderboardState.activeTab === "overall" ? "text-[var(--accent-primary)]" : "text-amber-500"} />
                <span>{t("header.leaderboardTabOverall")}</span>
              </button>

              <button
                type="button"
                onClick={() => switchLeaderboardTab("coding")}
                class={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-medium transition-all cursor-pointer ${
                  leaderboardState.activeTab === "coding"
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
              >
                <Cpu size={13} class={leaderboardState.activeTab === "coding" ? "text-[var(--accent-primary)]" : "text-indigo-400"} />
                <span>{t("header.leaderboardTabCoding")}</span>
              </button>

              <button
                type="button"
                onClick={() => switchLeaderboardTab("speed")}
                class={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-medium transition-all cursor-pointer ${
                  leaderboardState.activeTab === "speed"
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
              >
                <Zap size={13} class={leaderboardState.activeTab === "speed" ? "text-[var(--accent-primary)]" : "text-cyan-400"} />
                <span>{t("header.leaderboardTabSpeed")}</span>
              </button>

              <button
                type="button"
                onClick={() => switchLeaderboardTab("price")}
                class={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-medium transition-all cursor-pointer ${
                  leaderboardState.activeTab === "price"
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
              >
                <Coins size={13} class={leaderboardState.activeTab === "price" ? "text-[var(--accent-primary)]" : "text-emerald-400"} />
                <span>{t("header.leaderboardTabPrice")}</span>
              </button>

              <button
                type="button"
                onClick={() => switchLeaderboardTab("gateways")}
                class={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-medium transition-all cursor-pointer ${
                  leaderboardState.activeTab === "gateways"
                    ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-xs"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
              >
                <Radio size={13} />
                <span>{t("header.leaderboardTabGateways")}</span>
              </button>
            </div>
          </div>

          {/* Body Content */}
          <div class="flex-1 overflow-y-auto p-4 space-y-2.5 min-h-[300px]">
            <Show when={leaderboardState.loading}>
              <div class="flex flex-col items-center justify-center py-16 gap-3 text-[var(--text-muted)]">
                <Spinner class="h-6 w-6 text-[var(--accent-primary)]" />
                <span class="text-xs">{t("common.loading")}</span>
              </div>
            </Show>

            {/* Models Table / List */}
            <Show when={!leaderboardState.loading && leaderboardState.activeTab !== "gateways"}>
              <div class="space-y-2">
                <For each={leaderboardState.models}>
                  {(m) => {
                    const active = isCurrentModel(m.apiName, m.slug);
                    return (
                      <div
                        class={`group rounded-xl border p-3 flex items-center justify-between gap-3 transition-all ${
                          active
                            ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/20"
                            : "border-[var(--border-subtle)] bg-[var(--bg-inset)]/30 hover:border-[var(--border-strong)] hover:bg-[var(--bg-hover)]/40"
                        }`}
                      >
                        {/* Rank Badge + Logo + Name */}
                        <div class="flex items-center gap-3 min-w-0 flex-1">
                          <span
                            class={`flex h-6 w-6 shrink-0 items-center justify-center rounded-lg text-[11px] font-black ${
                              m.rank === 1
                                ? "bg-gradient-to-br from-amber-400 to-amber-500 text-slate-950 shadow-xs"
                                : m.rank === 2
                                ? "bg-slate-300 text-slate-900"
                                : m.rank === 3
                                ? "bg-amber-700 text-white"
                                : "bg-[var(--bg-panel)] border border-[var(--border-subtle)] text-[var(--text-muted)]"
                            }`}
                          >
                            {m.rank}
                          </span>

                          <ProviderIcon id={m.vendor} class="w-4 h-4 shrink-0" />

                          <div class="min-w-0 flex-1">
                            <div class="flex items-center gap-1.5 flex-wrap">
                              <span class="font-bold text-[13px] text-[var(--text-primary)]">
                                {m.name}
                              </span>
                              <Show when={m.badges && m.badges.length > 0}>
                                <span class="rounded bg-emerald-500/10 text-emerald-500 border border-emerald-500/20 px-1 py-0.2 text-[11px] font-medium">
                                  {m.badges[0]}
                                </span>
                              </Show>
                              <Show when={m.reasoning}>
                                <span class="rounded bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 px-1 py-0.2 text-[11px] font-medium">
                                  Thinking
                                </span>
                              </Show>
                            </div>
                            <div class="text-[11px] text-[var(--text-muted)] truncate mt-0.5 font-mono">
                              {m.summary || m.apiName}
                            </div>
                          </div>
                        </div>

                        {/* Scores & Metrics */}
                        <div class="flex items-center gap-4 shrink-0 text-right">
                          <div class="hidden sm:block">
                            <div class="text-[11px] text-[var(--text-muted)]">
                              {t("header.leaderboardScoreIndex")}
                            </div>
                            <div class="font-bold font-mono text-[13px] text-amber-500">
                              {m.scores?.overall || m.scores?.coding || "—"}
                            </div>
                          </div>

                          <div class="hidden sm:block">
                            <div class="text-[11px] text-[var(--text-muted)]">
                              {t("header.leaderboardSpeedTps")}
                            </div>
                            <div class="font-mono text-[12px] text-[var(--text-primary)]">
                              {m.perf?.tps ? `${m.perf.tps} tps` : "—"}
                            </div>
                          </div>

                          <div>
                            <div class="text-[11px] text-[var(--text-muted)]">
                              {(() => {
                                const sym = m.price?.currency?.toUpperCase() === "CNY" ? "¥" : "$";
                                return `${sym}${m.price?.input || 0} / ${sym}${m.price?.output || 0}`;
                              })()}
                            </div>
                            <div class="text-[11px] text-[var(--text-faint)]">
                              {t("header.leaderboardPerMTokens")}
                            </div>
                          </div>

                          {/* Action Button */}
                          <div class="w-24 flex justify-end">
                            <Show
                              when={!active}
                              fallback={
                                <span class="inline-flex items-center gap-1 text-[11px] font-semibold text-emerald-500 px-2 py-1 bg-emerald-500/10 rounded-lg">
                                  <Check size={12} />
                                  {t("header.leaderboardActiveModelBadge")}
                                </span>
                              }
                            >
                              <button
                                type="button"
                                disabled={!canApplyModel(m.vendor, m.apiName || m.slug || "")}
                                onClick={() => handleApplyModel(m.vendor, m.apiName, m.slug)}
                                class="inline-flex items-center justify-center rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:border-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                              >
                                {t("header.leaderboardSetAsActiveModel")}
                              </button>
                            </Show>
                          </div>
                        </div>
                      </div>
                    );
                  }}
                </For>
              </div>
            </Show>

            {/* Gateway directory only; account login does not connect inference. */}
            <Show when={leaderboardState.activeTab === "gateways" && leaderboardState.gatewaysError && leaderboardState.gateways.length === 0}>
              <div class="flex items-start justify-between gap-3 rounded-2xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-4 py-3">
                <span class="min-w-0 text-[12px] leading-relaxed text-[var(--danger-text)]">
                  {t("models.gatewaysLoadError")}
                </span>
                <button
                  type="button"
                  onClick={() => void fetchGateways()}
                  class="shrink-0 rounded-lg border border-[var(--danger-border)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  {t("common.refresh")}
                </button>
              </div>
            </Show>

            <Show when={!leaderboardState.loading && leaderboardState.activeTab === "gateways"}>
              <div class="space-y-4">
                <For each={leaderboardState.gateways}>
                  {(gw) => (
                    <div class="rounded-2xl border border-[var(--accent-border)] bg-gradient-to-b from-[var(--accent-surface)] to-[var(--bg-panel)] p-5 space-y-4 shadow-sm">
                      <div class="flex items-start justify-between gap-3">
                        <div>
                          <div class="flex items-center gap-2">
                            <span class="text-[16px] font-bold text-[var(--text-primary)]">
                              {gw.name}
                            </span>
                          </div>
                          <p class="text-[12px] text-[var(--text-muted)] mt-1 leading-relaxed">
                            {gw.domain}
                          </p>
                        </div>
                      </div>

                      {/* 3 Metric Pills */}
                      <div class="grid grid-cols-3 gap-2.5">
                        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5">
                          <div class="text-[11px] text-[var(--text-muted)]">
                            {t("header.gatewaySavingsLabel")}
                          </div>
                          <div class="font-extrabold text-[16px] text-emerald-500 font-mono mt-0.5">
                            {gw.savingsPercent}% OFF
                          </div>
                        </div>

                        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5">
                          <div class="text-[11px] text-[var(--text-muted)]">
                            {t("header.gatewayUptimeLabel")}
                          </div>
                          <div class="font-extrabold text-[16px] text-[var(--text-primary)] font-mono mt-0.5">
                            {gw.uptime}
                          </div>
                        </div>

                        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5">
                          <div class="text-[11px] text-[var(--text-muted)]">
                            {t("header.gatewayLatencyLabel")}
                          </div>
                          <div class="font-extrabold text-[16px] text-cyan-400 font-mono mt-0.5">
                            ~{gw.latencyMs}ms
                          </div>
                        </div>
                      </div>

                      {/* Features & Models */}
                      <div class="pt-2 border-t border-[var(--border-subtle)]/70 flex flex-wrap gap-2">
                        <For each={gw.features}>
                          {(f) => (
                            <span class="inline-flex items-center gap-1 text-[11px] text-[var(--text-secondary)] bg-[var(--bg-panel)] border border-[var(--border-subtle)] px-2 py-0.8 rounded-md">
                              <Check size={11} class="text-emerald-500" />
                              {f}
                            </span>
                          )}
                        </For>
                      </div>
                    </div>
                  )}
                </For>
              </div>
            </Show>
          </div>

          {/* Footer with External Link */}
          <div class="px-5 py-3 border-t border-[var(--border-subtle)] flex items-center justify-between gap-3 bg-[var(--bg-panel)]">
            <button
              type="button"
              onClick={handleOpenOfficialWeb}
              class="inline-flex items-center gap-1.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--accent-text)] transition-colors cursor-pointer"
            >
              <Globe size={13} />
              <span>{t("header.leaderboardViewOnWeb")}</span>
              <ExternalLink size={11} class="opacity-60" />
            </button>

            <button
              type="button"
              onClick={() => closeLeaderboardModal()}
              class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              {t("common.close")}
            </button>
          </div>
        </div>
      </div>
      )}
    </Presence>
  );
}
