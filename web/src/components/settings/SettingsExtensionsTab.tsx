// web/src/components/settings/SettingsExtensionsTab.tsx
import { createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { Cpu, ExternalLink, Plus, Sparkles, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  closeSettingsModal,
  deleteMcpServer,
  fetchGlobalConfig,
  saveMcpServer,
  setWorkspace,
} from "../../store/workspace";
import SkillsInspectorView from "../skills/SkillsInspectorView";
import { DataProfilesSection } from "./DataProfilesSection";
import { RemoteHostsSection } from "./RemoteHostsSection";
import { SettingGroup } from "./setting-controls";

export function SettingsExtensionsTab() {
  const [mcpServers, setMcpServers] = createSignal<Record<string, any>>({});
  const [showAddMcp, setShowAddMcp] = createSignal(false);
  const [mcpName, setMcpName] = createSignal("");
  const [mcpType, setMcpType] = createSignal<"local" | "remote">("local");
  const [mcpTarget, setMcpTarget] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);
  let feedbackTimer: ReturnType<typeof setTimeout> | undefined;

  const setTimedFeedback = (next: { ok: boolean; text: string }) => {
    if (feedbackTimer !== undefined) clearTimeout(feedbackTimer);
    setFeedback(next);
    if (next.ok) {
      feedbackTimer = setTimeout(() => {
        feedbackTimer = undefined;
        setFeedback(null);
      }, 3000);
    }
  };
  onCleanup(() => {
    if (feedbackTimer !== undefined) clearTimeout(feedbackTimer);
  });

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
      setShowAddMcp(false);
      setTimedFeedback({ ok: true, text: t("models.mcpAdded", { id }) });
    } catch (err: any) {
      setTimedFeedback({ ok: false, text: err?.message || t("models.mcpAddFailed") });
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
      setTimedFeedback({ ok: true, text: t("models.mcpRemoved", { id }) });
    } catch (err: any) {
      setTimedFeedback({ ok: false, text: err?.message || t("models.mcpRemoveFailed") });
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
      {/* 1. MCP Servers */}
      <SettingGroup
        title={t("models.mcpTitle")}
        tip={t("models.mcpSubtitle")}
        action={
          <button
            type="button"
            onClick={() => setShowAddMcp((v) => !v)}
            class="hoxi-press inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Show when={!showAddMcp()}>
              <Plus size={12} />
            </Show>
            <span>{showAddMcp() ? t("common.cancel") : t("models.add")}</span>
          </button>
        }
      >
        <Show when={showAddMcp()}>
          <form onSubmit={(e) => void onAddMcpServer(e)} class="flex flex-wrap items-center gap-2 p-4">
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
                  : "https://hoxi.ai/api/mcp"
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
        </Show>

        <Show
          when={Object.keys(mcpServers()).length > 0}
          fallback={
            <Show when={!showAddMcp()}>
              <p class="px-5 py-3.5 text-[12.5px] text-[var(--text-muted)]">
                {t("models.noMcpServers")}
              </p>
            </Show>
          }
        >
          <div class="p-4 space-y-1.5">
            <For each={Object.entries(mcpServers())}>
              {([id, srv]) => (
                <div class="flex items-center justify-between rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-3.5 py-2">
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
                    aria-label={t("models.removeMcpTooltip")}
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>

        <Show when={feedback()}>
          <div class="px-4 py-2.5">
            <div
              class={`rounded-xl border px-3.5 py-2 text-[12px] ${
                feedback()!.ok
                  ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                  : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              }`}
            >
              {feedback()!.text}
            </div>
          </div>
        </Show>
      </SettingGroup>

      {/* 2. Credential-Isolated Data & SSH Profiles */}
      <DataProfilesSection />

      {/* 3. Cross-Platform Headless Dev Hosts (hoxi host) */}
      <RemoteHostsSection />

      {/* 4. Agent Skills (Local + Marketplace) */}
      <SettingGroup
        title={t("skills.title")}
        tip={t("settings.extensionsSubtitle")}
        action={
          <button
            type="button"
            onClick={openSkillsInWorkspaceInspector}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer shrink-0"
          >
            <Sparkles size={12} class="text-[var(--accent-primary)]" />
            <span>{t("settings.openSkillsInInspectorBtn")}</span>
            <ExternalLink size={11} />
          </button>
        }
      >
        <div class="h-[520px] flex flex-col overflow-hidden">
          <SkillsInspectorView alwaysActive />
        </div>
      </SettingGroup>
    </div>
  );
}
