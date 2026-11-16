// web/src/components/settings/SettingsExtensionsTab.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { Blocks, Cpu, ExternalLink, Plus, Sparkles, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  closeSettingsModal,
  deleteMcpServer,
  fetchGlobalConfig,
  saveMcpServer,
  setWorkspace,
} from "../../store/workspace";
import SkillsInspectorView from "../skills/SkillsInspectorView";
import { SettingGroup } from "./setting-controls";

export function SettingsExtensionsTab() {
  const [mcpServers, setMcpServers] = createSignal<Record<string, any>>({});
  const [mcpName, setMcpName] = createSignal("");
  const [mcpType, setMcpType] = createSignal<"local" | "remote">("local");
  const [mcpTarget, setMcpTarget] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  onMount(async () => {
    try {
      const cfg = await fetchGlobalConfig();
      if (cfg?.mcp && typeof cfg.mcp === "object") {
        setMcpServers(cfg.mcp);
      }
    } catch {
      /* ignore */
    }
  });

  const onAddMcpServer = async (ev: Event) => {
    ev.preventDefault();
    const id = mcpName().trim().replace(/[^a-zA-Z0-9_-]/g, "-");
    const target = mcpTarget().trim();
    if (!id || !target || saving()) return;
    setSaving(true);
    setFeedback(null);
    try {
      const entry =
        mcpType() === "local"
          ? { type: "local", command: target.split(/\s+/), enabled: true }
          : { type: "remote", url: target, enabled: true };
      const cfg = await saveMcpServer(id, entry);
      setMcpServers(cfg?.mcp && typeof cfg.mcp === "object" ? cfg.mcp : { [id]: entry });
      setMcpName("");
      setMcpTarget("");
      setFeedback({ ok: true, text: t("models.mcpAdded", { id }) });
      setTimeout(() => setFeedback(null), 3000);
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.mcpAddFailed") });
    } finally {
      setSaving(false);
    }
  };

  const onRemoveMcpServer = async (id: string) => {
    if (saving()) return;
    setSaving(true);
    try {
      const cfg = await deleteMcpServer(id);
      setMcpServers(cfg?.mcp && typeof cfg.mcp === "object" ? cfg.mcp : {});
      setFeedback({ ok: true, text: t("models.mcpRemoved", { id }) });
      setTimeout(() => setFeedback(null), 3000);
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.mcpRemoveFailed") });
    } finally {
      setSaving(false);
    }
  };

  const openSkillsInWorkspaceInspector = () => {
    closeSettingsModal();
    setWorkspace({ inspectorOpen: true, inspectorTab: "skills" });
  };

  return (
    <div class="space-y-6">
      {/* Header */}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
        <div class="flex items-center gap-3 min-w-0">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <Blocks size={16} />
          </div>
          <div class="min-w-0">
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("settings.extensionsTitle")}
            </h3>
            <p class="text-[12px] text-[var(--text-secondary)]">
              {t("settings.extensionsSubtitle")}
            </p>
          </div>
        </div>
        <button
          type="button"
          onClick={openSkillsInWorkspaceInspector}
          class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer shrink-0"
        >
          <Sparkles size={13} class="text-[var(--accent-primary)]" />
          <span>{t("settings.openSkillsInInspectorBtn")}</span>
          <ExternalLink size={12} />
        </button>
      </div>

      {/* 1. MCP Servers */}
      <SettingGroup
        title={t("models.mcpTitle")}
        tip={t("models.mcpSubtitle")}
      >
        <div class="p-4 space-y-3">
          <form onSubmit={(e) => void onAddMcpServer(e)} class="flex flex-wrap items-center gap-2">
            <input
              type="text"
              placeholder={t("models.mcpServerId")}
              value={mcpName()}
              onInput={(e) => setMcpName(e.currentTarget.value)}
              class="w-40 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
            <select
              value={mcpType()}
              onChange={(e) => setMcpType(e.currentTarget.value as "local" | "remote")}
              class="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none"
            >
              <option value="local">{t("models.localCommand")}</option>
              <option value="remote">{t("models.remoteUrl")}</option>
            </select>
            <input
              type="text"
              placeholder={
                mcpType() === "local"
                  ? "npx -y @modelcontextprotocol/server-filesystem ."
                  : "https://mcp.hoxi.ai/sse"
              }
              value={mcpTarget()}
              onInput={(e) => setMcpTarget(e.currentTarget.value)}
              class="min-w-[220px] flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
            <button
              type="submit"
              disabled={saving() || !mcpName().trim() || !mcpTarget().trim()}
              class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
            >
              <Plus size={13} />
              {t("models.add")}
            </button>
          </form>

          <div class="space-y-1.5">
            <Show
              when={Object.keys(mcpServers()).length > 0}
              fallback={
                <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
                  {t("models.noMcpServers")}
                </div>
              }
            >
              <For each={Object.entries(mcpServers())}>
                {([id, srv]) => (
                  <div class="flex items-center justify-between rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-2">
                    <div class="flex items-center gap-2.5 min-w-0">
                      <Cpu size={14} class="text-[var(--accent-primary)] shrink-0" />
                      <span class="font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                        {id}
                      </span>
                      <span class="truncate font-mono text-[11px] text-[var(--text-muted)]">
                        {Array.isArray(srv?.command)
                          ? srv.command.join(" ")
                          : srv?.url || srv?.type || "mcp"}
                      </span>
                    </div>
                    <button
                      type="button"
                      onClick={() => void onRemoveMcpServer(id)}
                      class="text-[var(--text-muted)] hover:text-[var(--danger-text)] p-1 cursor-pointer"
                      title={t("models.removeMcpTooltip")}
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                )}
              </For>
            </Show>
          </div>

          <Show when={feedback()}>
            <div
              class={`rounded-xl border px-3.5 py-2 text-[12px] ${
                feedback()!.ok
                  ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                  : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              }`}
            >
              {feedback()!.text}
            </div>
          </Show>
        </div>
      </SettingGroup>

      {/* 2. Agent Skills (Local + Marketplace) */}
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/35 overflow-hidden h-[540px] flex flex-col">
        <SkillsInspectorView />
      </div>
    </div>
  );
}
