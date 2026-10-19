// web/src/components/models/MultiModelArenaModal.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, GitBranchPlus, LoaderCircle, Sparkles, Swords, X } from "lucide-solid";
import {
  newIsolatedSession,
  selectSession,
  setWorkspace,
  submitSessionPrompt,
  workspace,
  type SelectedModel,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { isModelFree } from "./model-selector-utils";

interface FlatModelOption {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  key: string;
  free: boolean;
  multiplier?: string;
}

/**
 * Ported from kilocode MultiModelSelector.tsx & new-worktree-models.ts:
 * Spawns 2–4 isolated Git Worktrees in parallel, running the exact same prompt
 * across multiple AI models so the user can compare diffs side-by-side.
 */
export default function MultiModelArenaModal() {
  const [selectedKeys, setSelectedKeys] = createSignal<string[]>([]);
  const [prompt, setPrompt] = createSignal("");
  const [launching, setLaunching] = createSignal(false);
  const [search, setSearch] = createSignal("");

  const availableModels = createMemo<FlatModelOption[]>(() => {
    const connected = new Set(workspace.connectedProviders);
    const list: FlatModelOption[] = [];
    for (const p of workspace.providers) {
      if (connected.size > 0 && !connected.has(p.id) && p.id !== "opencode") continue;
      for (const [mId, mInfo] of Object.entries(p.models || {})) {
        const free = isModelFree(p.id, mId, mInfo);
        if (!connected.has(p.id) && !(p.id === "opencode" && free)) continue;
        list.push({
          providerID: p.id,
          providerName: p.name || p.id,
          modelID: mId,
          modelName: mInfo.name || mId,
          key: `${p.id}/${mId}`,
          free,
          multiplier: mInfo?.multiplier,
        });
      }
    }
    const q = search().trim().toLowerCase();
    if (!q) return list.slice(0, 60);
    return list
      .filter(
        (m) =>
          m.modelName.toLowerCase().includes(q) ||
          m.modelID.toLowerCase().includes(q) ||
          m.providerName.toLowerCase().includes(q)
      )
      .slice(0, 60);
  });

  const toggleModel = (key: string) => {
    setSelectedKeys((prev) => {
      if (prev.includes(key)) return prev.filter((k) => k !== key);
      if (prev.length >= 4) return prev;
      return [...prev, key];
    });
  };

  const close = () => {
    if (launching()) return;
    setWorkspace("arenaModalOpen", false);
  };

  const launchArena = async () => {
    const keys = selectedKeys();
    const taskPrompt = prompt().trim();
    if (keys.length < 1 || !taskPrompt || launching()) return;

    setLaunching(true);
    try {
      const models: SelectedModel[] = keys.map((k) => {
        const [providerID, ...rest] = k.split("/");
        return { providerID, modelID: rest.join("/") };
      });

      let firstSessionID: string | null = null;
      const baseTitle = taskPrompt.slice(0, 32);

      // Provision isolated Git Worktree sessions and dispatch prompts in parallel
      await Promise.all(
        models.map(async (model, idx) => {
          const shortModel = model.modelID.split("/").pop() || model.modelID;
          const title = `[Arena · ${shortModel}] ${baseTitle}`;
          const sessionID = await newIsolatedSession(title, { isolated: true });
          if (idx === 0) firstSessionID = sessionID;
          await submitSessionPrompt(taskPrompt, sessionID, {
            agent: workspace.activeMode,
            model,
          });
        })
      );

      if (firstSessionID) {
        await selectSession(firstSessionID);
      }
      setPrompt("");
      setSelectedKeys([]);
      setWorkspace("arenaModalOpen", false);
    } catch (err) {
      console.error("launchArena failed:", err);
    } finally {
      setLaunching(false);
    }
  };

  return (
    <Show when={workspace.arenaModalOpen}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
        onClick={close}
      >
        <div
          class="w-full max-w-xl rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl overflow-hidden flex flex-col max-h-[85vh]"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div class="flex items-center justify-between px-5 py-3.5 border-b border-[var(--border-subtle)]">
            <div class="flex items-center gap-2.5">
              <div class="flex h-7 w-7 items-center justify-center rounded-lg bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <Swords size={15} />
              </div>
              <div>
                <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
                  {t("models.arenaTitle")}
                </h3>
                <p class="text-[11px] text-[var(--text-muted)]">
                  {t("models.arenaSubtitle")}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={close}
              class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={16} />
            </button>
          </div>

          {/* Body */}
          <div class="p-5 space-y-4 overflow-y-auto flex-1">
            {/* Prompt Input */}
            <div class="space-y-1.5">
              <label class="text-[11.5px] font-semibold text-[var(--text-secondary)]">
                {t("models.stepPrompt")}
              </label>
              <textarea
                rows={3}
                value={prompt()}
                onInput={(ev) => setPrompt(ev.currentTarget.value)}
                placeholder={t("models.promptPlaceholder")}
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none resize-none"
              />
            </div>

            {/* Model Multi-Select */}
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <label class="text-[11.5px] font-semibold text-[var(--text-secondary)]">
                  {t("models.stepSelectModels", { count: selectedKeys().length })}
                </label>
                <input
                  type="text"
                  value={search()}
                  onInput={(ev) => setSearch(ev.currentTarget.value)}
                  placeholder={t("models.filterModelsPlaceholder")}
                  class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none w-44"
                />
              </div>

              <div class="grid grid-cols-1 sm:grid-cols-2 gap-1.5 max-h-56 overflow-y-auto pr-1">
                <For each={availableModels()}>
                  {(m) => {
                    const active = () => selectedKeys().includes(m.key);
                    return (
                      <button
                        type="button"
                        onClick={() => toggleModel(m.key)}
                        class={`flex items-center justify-between gap-2 rounded-xl border px-3 py-2 text-left transition-all cursor-pointer ${
                          active()
                            ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/65 text-[var(--text-primary)]"
                            : "border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                        }`}
                      >
                        <div class="flex items-center gap-2 min-w-0">
                          <ProviderIcon id={m.providerID} class="w-4 h-4 shrink-0" />
                          <div class="min-w-0">
                            <div class="flex items-center gap-1.5 text-[12px] font-medium text-[var(--text-primary)]">
                              <span class="truncate">{m.modelName}</span>
                              <Show when={m.free}>
                                <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1 py-[1px] text-[9px] font-semibold text-[var(--ok-text)]">
                                  {t("models.freeModel")}
                                </span>
                              </Show>
                              <Show when={m.multiplier}>
                                <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1 py-[1px] font-mono text-[9px] font-semibold text-[var(--text-secondary)]">
                                  {m.multiplier}
                                </span>
                              </Show>
                            </div>
                            <div class="text-[10.5px] text-[var(--text-muted)] truncate">
                              {m.providerName}
                            </div>
                          </div>
                        </div>
                        <span
                          class={`flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
                            active()
                              ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                              : "border-[var(--border-strong)]"
                          }`}
                        >
                          <Show when={active()}>
                            <Check size={10} strokeWidth={3} />
                          </Show>
                        </span>
                      </button>
                    );
                  }}
                </For>
              </div>
            </div>
          </div>

          {/* Footer */}
          <div class="flex items-center justify-between px-5 py-3 border-t border-[var(--border-subtle)] bg-[var(--bg-panel)]/50">
            <div class="flex items-center gap-1.5 text-[11px] text-[var(--text-muted)]">
              <GitBranchPlus size={13} class="text-[var(--accent-primary)]" />
              <span>
                {t("models.worktreePlanNotice", { count: selectedKeys().length })}
              </span>
            </div>

            <div class="flex items-center gap-2">
              <button
                type="button"
                onClick={close}
                class="rounded-lg px-3 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                disabled={selectedKeys().length === 0 || !prompt().trim() || launching()}
                onClick={() => void launchArena()}
                class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
              >
                <Show
                  when={!launching()}
                  fallback={<LoaderCircle size={13} class="animate-spin" />}
                >
                  <Sparkles size={13} />
                </Show>
                <span>
                  {t("models.launchArena", { count: selectedKeys().length })}
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}
