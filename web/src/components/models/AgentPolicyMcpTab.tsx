// web/src/components/models/AgentPolicyMcpTab.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { Check, Cpu, Plus, Trash2 } from "lucide-solid";
import { hoxiAPI } from "../../services/api";

type PermMode = "ask" | "allow" | "deny";

export default function AgentPolicyMcpTab() {
  const [permEdit, setPermEdit] = createSignal<PermMode>("allow");
  const [permBash, setPermBash] = createSignal<PermMode>("ask");
  const [permWebfetch, setPermWebfetch] = createSignal<PermMode>("allow");
  const [mcpServers, setMcpServers] = createSignal<Record<string, any>>({});
  const [mcpName, setMcpName] = createSignal("");
  const [mcpType, setMcpType] = createSignal<"local" | "remote">("local");
  const [mcpTarget, setMcpTarget] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  onMount(async () => {
    try {
      const cfg = await hoxiAPI.getGlobalConfig();
      const perm = cfg?.permission ?? {};
      if (perm.edit === "ask" || perm.edit === "allow" || perm.edit === "deny") {
        setPermEdit(perm.edit);
      }
      if (perm.bash === "ask" || perm.bash === "allow" || perm.bash === "deny") {
        setPermBash(perm.bash);
      }
      if (perm.webfetch === "ask" || perm.webfetch === "allow" || perm.webfetch === "deny") {
        setPermWebfetch(perm.webfetch);
      }
      if (cfg?.mcp && typeof cfg.mcp === "object") {
        setMcpServers(cfg.mcp);
      }
    } catch {
      /* ignore */
    }
  });

  const onSavePermissions = async () => {
    if (saving()) return;
    setSaving(true);
    setFeedback(null);
    try {
      const cfg = await hoxiAPI.getGlobalConfig();
      const nextPerm = {
        ...(cfg?.permission ?? {}),
        edit: permEdit(),
        bash: permBash(),
        webfetch: permWebfetch(),
      };
      await hoxiAPI.updateGlobalConfig({ ...cfg, permission: nextPerm });
      setFeedback({ ok: true, text: "Updated Auto-Approve permission policy!" });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to update permissions." });
    } finally {
      setSaving(false);
    }
  };

  const onAddMcpServer = async (ev: Event) => {
    ev.preventDefault();
    const id = mcpName().trim().replace(/[^a-zA-Z0-9_-]/g, "-");
    const target = mcpTarget().trim();
    if (!id || !target || saving()) return;
    setSaving(true);
    setFeedback(null);
    try {
      const cfg = await hoxiAPI.getGlobalConfig();
      const entry =
        mcpType() === "local"
          ? { type: "local", command: target.split(/\s+/), enabled: true }
          : { type: "remote", url: target, enabled: true };
      const nextMcp = { ...(cfg?.mcp ?? {}), [id]: entry };
      await hoxiAPI.updateGlobalConfig({ ...cfg, mcp: nextMcp });
      setMcpServers(nextMcp);
      setMcpName("");
      setMcpTarget("");
      setFeedback({ ok: true, text: `Added MCP server "${id}"!` });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to add MCP server." });
    } finally {
      setSaving(false);
    }
  };

  const onRemoveMcpServer = async (id: string) => {
    if (saving()) return;
    setSaving(true);
    try {
      const cfg = await hoxiAPI.getGlobalConfig();
      const nextMcp = { ...(cfg?.mcp ?? {}) };
      delete nextMcp[id];
      await hoxiAPI.updateGlobalConfig({ ...cfg, mcp: nextMcp });
      setMcpServers(nextMcp);
      setFeedback({ ok: true, text: `Removed MCP server "${id}".` });
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || "Failed to remove MCP server." });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div class="flex-1 overflow-y-auto p-6 space-y-6">
      {/* Section 1: Auto-Approve Permissions */}
      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
              Auto-Approve Tool Policy
            </h3>
            <p class="text-[11.5px] text-[var(--text-muted)]">
              Control whether Hoxi asks for confirmation before executing tools
            </p>
          </div>
          <button
            type="button"
            disabled={saving()}
            onClick={() => void onSavePermissions()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-white hover:opacity-90 disabled:opacity-50 cursor-pointer"
          >
            <Check size={13} />
            Save Policy
          </button>
        </div>

        <div class="grid grid-cols-3 gap-3">
          {/* Edit / Write Policy */}
          <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 space-y-2">
            <div class="text-[12px] font-medium text-[var(--text-primary)]">
              File Edits & Writes
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">edit, write, patch</div>
            <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 w-full">
              <For each={["allow", "ask"] as PermMode[]}>
                {(m) => (
                  <button
                    type="button"
                    onClick={() => setPermEdit(m)}
                    class={`flex-1 rounded-md py-1 text-[11px] font-medium capitalize cursor-pointer ${
                      permEdit() === m
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                        : "text-[var(--text-muted)]"
                    }`}
                  >
                    {m === "allow" ? "Auto-Allow" : "Ask"}
                  </button>
                )}
              </For>
            </div>
          </div>

          {/* Shell / Bash Policy */}
          <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 space-y-2">
            <div class="text-[12px] font-medium text-[var(--text-primary)]">
              Shell Commands
            </div>
            <div class="text-[11px] text-[var(--text-muted)]">bash, terminal execution</div>
            <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 w-full">
              <For each={["allow", "ask"] as PermMode[]}>
                {(m) => (
                  <button
                    type="button"
                    onClick={() => setPermBash(m)}
                    class={`flex-1 rounded-md py-1 text-[11px] font-medium capitalize cursor-pointer ${
                      permBash() === m
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                        : "text-[var(--text-muted)]"
                    }`}
                  >
                    {m === "allow" ? "Auto-Allow" : "Ask"}
                  </button>
                )}
              </For>
            </div>
          </div>

          {/* WebFetch Policy */}
          <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 space-y-2">
            <div class="text-[12px] font-medium text-[var(--text-primary)]">Web Fetch</div>
            <div class="text-[11px] text-[var(--text-muted)]">webfetch, external docs</div>
            <div class="inline-flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 w-full">
              <For each={["allow", "ask"] as PermMode[]}>
                {(m) => (
                  <button
                    type="button"
                    onClick={() => setPermWebfetch(m)}
                    class={`flex-1 rounded-md py-1 text-[11px] font-medium capitalize cursor-pointer ${
                      permWebfetch() === m
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                        : "text-[var(--text-muted)]"
                    }`}
                  >
                    {m === "allow" ? "Auto-Allow" : "Ask"}
                  </button>
                )}
              </For>
            </div>
          </div>
        </div>
      </div>

      {/* Section 2: MCP Servers */}
      <div class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            MCP Servers (Model Context Protocol)
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            Connect local stdio or remote SSE/HTTP MCP tool servers
          </p>
        </div>

        <form onSubmit={onAddMcpServer} class="flex items-center gap-2">
          <input
            type="text"
            placeholder="Server ID (e.g. filesystem)"
            value={mcpName()}
            onInput={(e) => setMcpName(e.currentTarget.value)}
            class="w-40 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <select
            value={mcpType()}
            onChange={(e) => setMcpType(e.currentTarget.value as "local" | "remote")}
            class="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none"
          >
            <option value="local">Local Command</option>
            <option value="remote">Remote URL</option>
          </select>
          <input
            type="text"
            placeholder={
              mcpType() === "local"
                ? "npx -y @modelcontextprotocol/server-filesystem ."
                : "https://mcp.example.com/sse"
            }
            value={mcpTarget()}
            onInput={(e) => setMcpTarget(e.currentTarget.value)}
            class="flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
          />
          <button
            type="submit"
            disabled={saving() || !mcpName().trim() || !mcpTarget().trim()}
            class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-white disabled:opacity-40 cursor-pointer"
          >
            <Plus size={13} />
            Add
          </button>
        </form>

        <div class="space-y-1.5">
          <Show
            when={Object.keys(mcpServers()).length > 0}
            fallback={
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
                No MCP servers configured yet.
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
                    title="Remove MCP server"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              )}
            </For>
          </Show>
        </div>
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
  );
}
