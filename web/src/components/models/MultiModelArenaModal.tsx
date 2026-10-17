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
import { locale } from "../../i18n";

interface FlatModelOption {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  key: string;
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
      if (connected.size > 0 && !connected.has(p.id)) continue;
      for (const [mId, mInfo] of Object.entries(p.models || {})) {
        list.push({
          providerID: p.id,
          providerName: p.name || p.id,
          modelID: mId,
          modelName: mInfo.name || mId,
          key: `${p.id}/${mId}`,
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
          const sessionID = await newIsolatedSession(title);
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
                  {locale() === "zh-CN"
                    ? "多模型 Worktree 赛马场 (Multi-Model Arena)"
                    : "Multi-Model Worktree Arena"}
                </h3>
                <p class="text-[11px] text-[var(--text-muted)]">
                  {locale() === "zh-CN"
                    ? "为每个选中模型自动创建独立 Git Worktree 沙箱并同时执行同一任务"
                    : "Spawn isolated Git Worktrees in parallel across up to 4 models"}
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
                {locale() === "zh-CN" ? "1. 输入并行对比任务指令" : "1. Task Prompt"}
              </label>
              <textarea
                rows={3}
                value={prompt()}
                onInput={(ev) => setPrompt(ev.currentTarget.value)}
                placeholder={
                  locale() === "zh-CN"
                    ? "例如：重构认证中间件并补充单元测试，对比不同模型的实现质量..."
                    : "e.g., Refactor the auth middleware and add unit tests…"
                }
                class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-2 text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none resize-none"
              />
            </div>

            {/* Model Multi-Select */}
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <label class="text-[11.5px] font-semibold text-[var(--text-secondary)]">
                  {locale() === "zh-CN"
                    ? `2. 选择参赛模型 (${selectedKeys().length}/4)`
                    : `2. Select Competing Models (${selectedKeys().length}/4)`}
                </label>
                <input
                  type="text"
                  value={search()}
                  onInput={(ev) => setSearch(ev.currentTarget.value)}
                  placeholder={locale() === "zh-CN" ? "搜索模型..." : "Filter models…"}
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
                            <div class="text-[12px] font-medium truncate text-[var(--text-primary)]">
                              {m.modelName}
                            </div>
                            <div class="text-[10.5px] text-[var(--text-muted)] truncate">
                              {m.providerName}
                            </div>
                          </div>
                        </div>
                        <span
                          class={`flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
                            active()
                              ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-white"
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
                {locale() === "zh-CN"
                  ? `将创建 ${selectedKeys().length} 个隔离 Worktree 分支并行运行`
                  : `Will spawn ${selectedKeys().length} isolated Worktree branches`}
              </span>
            </div>

            <div class="flex items-center gap-2">
              <button
                type="button"
                onClick={close}
                class="rounded-lg px-3 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                {locale() === "zh-CN" ? "取消" : "Cancel"}
              </button>
              <button
                type="button"
                disabled={selectedKeys().length === 0 || !prompt().trim() || launching()}
                onClick={() => void launchArena()}
                class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-white hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
              >
                <Show
                  when={!launching()}
                  fallback={<LoaderCircle size={13} class="animate-spin" />}
                >
                  <Sparkles size={13} />
                </Show>
                <span>
                  {locale() === "zh-CN"
                    ? `并发启动 (${selectedKeys().length})`
                    : `Launch Arena (${selectedKeys().length})`}
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Show>
  );
}
