// web/src/components/models/LocalCliBridgeBanner.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { ArrowRightLeft, Check, RefreshCw, Sparkles, Terminal } from "lucide-solid";
import { hoxiAPI, type LocalCLIStatusItem } from "../../services/api";
import { openHandoffModal, setWorkspace, syncLocalCLIModel } from "../../store/workspace";
import { t } from "../../i18n";

export default function LocalCliBridgeBanner() {
  const [tools, setTools] = createSignal<LocalCLIStatusItem[]>([]);
  const [busyID, setBusyID] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);

  const load = async () => {
    try {
      const res = await hoxiAPI.getLocalCLIStatus();
      setTools((res.tools || []).filter((item) => item.installed));
    } catch {
      /* ignore */
    }
  };

  onMount(() => void load());

  const onImport = async (tool: LocalCLIStatusItem) => {
    if (busyID()) return;
    setBusyID(tool.id);
    setErrorMsg(null);
    try {
      const res = await syncLocalCLIModel(tool);
      setTools(res.tools.filter((item) => item.installed));
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyID(null);
    }
  };

  return (
    <Show when={tools().length > 0}>
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 p-4 space-y-3">
        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-2.5 min-w-0">
            <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
              <Terminal size={15} />
            </div>
            <div class="min-w-0">
              <div class="text-[13px] font-semibold text-[var(--text-primary)]">
                {t("leftNav.handoffCliBannerTitle")}
              </div>
              <div class="text-[11.5px] text-[var(--text-secondary)] line-clamp-1">
                {t("leftNav.handoffCliBannerDesc")}
              </div>
            </div>
          </div>

          <button
            type="button"
            onClick={() => {
              setWorkspace("providerModalOpen", false);
              openHandoffModal();
            }}
            class="shrink-0 inline-flex items-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/50 px-3 py-1.5 text-[11.5px] font-semibold text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
          >
            <ArrowRightLeft size={13} />
            {t("leftNav.handoffBtn")}
          </button>
        </div>

        <div class="grid grid-cols-3 gap-2.5">
          <For each={tools()}>
            {(tool) => (
              <div class="flex items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2">
                <div class="min-w-0">
                  <div class="text-[12px] font-semibold text-[var(--text-primary)] truncate">
                    {tool.name}
                  </div>
                  <div class="text-[10.5px] font-mono text-[var(--text-muted)]">
                    {t("leftNav.handoffCliSessionsBadge", { count: tool.sessionCount })}
                  </div>
                </div>

                <Show
                  when={!tool.connected && tool.authenticated}
                  fallback={
                    <Show
                      when={tool.connected}
                      fallback={
                        <Show when={tool.sessionCount > 0 || tool.authMode === "session-handoff"}>
                          <button
                            type="button"
                            onClick={() => {
                              setWorkspace("providerModalOpen", false);
                              openHandoffModal();
                            }}
                            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-1 text-[10.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] shrink-0 cursor-pointer"
                          >
                            <ArrowRightLeft size={10} />
                            {t("leftNav.handoffBtn")}
                          </button>
                        </Show>
                      }
                    >
                      <div class="flex items-center gap-1 shrink-0">
                        <span class="inline-flex items-center gap-1 rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[10px] font-semibold text-[var(--ok-text)]">
                          <Check size={10} />
                          {t("leftNav.handoffCliConnected")}
                        </span>
                        <button
                          type="button"
                          disabled={busyID() === tool.id}
                          onClick={() => void onImport(tool)}
                          class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                          title={t("leftNav.handoffCliSyncModel")}
                        >
                          <RefreshCw
                            size={11}
                            class={busyID() === tool.id ? "animate-spin" : ""}
                          />
                        </button>
                      </div>
                    </Show>
                  }
                >
                  <button
                    type="button"
                    disabled={busyID() === tool.id}
                    onClick={() => void onImport(tool)}
                    class="inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 shrink-0 cursor-pointer"
                  >
                    <Sparkles size={11} />
                    {busyID() === tool.id
                      ? t("leftNav.handoffCliImporting")
                      : t("leftNav.handoffCliImportBtn")}
                  </button>
                </Show>
              </div>
            )}
          </For>
        </div>

        <Show when={errorMsg()}>
          <div class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)]/40 px-3 py-1.5 text-[11px] text-[var(--danger-text)]">
            {errorMsg()}
          </div>
        </Show>
      </div>
    </Show>
  );
}
