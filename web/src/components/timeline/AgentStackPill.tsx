// web/src/components/timeline/AgentStackPill.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  Bot,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  LoaderCircle,
  ShieldAlert,
  Square,
} from "lucide-solid";
import type { ToolPart } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import {
  isSessionBusy,
  selectSession,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";

export interface SubagentStackItem {
  sessionID: string;
  description: string;
  agentType: string;
  status: "running" | "completed" | "error";
  needsAttention: boolean;
}

/**
 * Ported from kilocode AgentStack.tsx & background-agents.ts:
 * Tracks active and recent child subagents spawned from the current session,
 * allowing 1-click inspection, individual stop, or Stop All.
 */
export default function AgentStackPill() {
  const [expanded, setExpanded] = createSignal(false);
  const [stoppingID, setStoppingID] = createSignal<string | null>(null);

  const subagents = createMemo<SubagentStackItem[]>(() => {
    const parentID = workspace.activeSessionID;
    if (!parentID) return [];

    const map = new Map<string, SubagentStackItem>();

    // 1. Scan `task` tool parts in the active session
    const turns = workspace.messagesBySession[parentID] ?? [];
    for (const turn of turns) {
      for (const part of turn.parts) {
        if (part.type !== "tool") continue;
        const tp = part as ToolPart;
        if ((tp.tool || "").toLowerCase() !== "task") continue;
        const meta = tp.state.metadata ?? {};
        const subId =
          (typeof meta.sessionId === "string" && meta.sessionId) ||
          (typeof meta.sessionID === "string" && meta.sessionID) ||
          null;
        if (!subId) continue;

        const childBusy = isSessionBusy(subId);
        const rawStatus = tp.state.status;
        const status: SubagentStackItem["status"] =
          childBusy || rawStatus === "running" || rawStatus === "pending"
            ? "running"
            : rawStatus === "error"
              ? "error"
              : "completed";

        const needsAttention =
          workspace.pendingPermissions.some((p) => p.sessionID === subId) ||
          workspace.pendingQuestions.some((q) => q.sessionID === subId);

        map.set(subId, {
          sessionID: subId,
          description:
            String(tp.state.input?.description || tp.state.title || "Subagent Task"),
          agentType: String(tp.state.input?.subagent_type || "general"),
          status,
          needsAttention,
        });
      }
    }

    // 2. Also include any child sessions whose `parentID === activeSessionID`
    for (const s of workspace.sessions) {
      if (s.parentID !== parentID || map.has(s.id)) continue;
      const childBusy = isSessionBusy(s.id);
      const needsAttention =
        workspace.pendingPermissions.some((p) => p.sessionID === s.id) ||
        workspace.pendingQuestions.some((q) => q.sessionID === s.id);
      map.set(s.id, {
        sessionID: s.id,
        description: s.title || s.id.slice(0, 10),
        agentType: "subagent",
        status: childBusy ? "running" : "completed",
        needsAttention,
      });
    }

    return Array.from(map.values()).sort((a, b) => {
      if (a.status === "running" && b.status !== "running") return -1;
      if (a.status !== "running" && b.status === "running") return 1;
      return 0;
    });
  });

  const runningCount = createMemo(
    () => subagents().filter((s) => s.status === "running").length
  );

  const attentionCount = createMemo(
    () => subagents().filter((s) => s.needsAttention).length
  );

  const stopSubagent = async (subID: string) => {
    if (stoppingID()) return;
    setStoppingID(subID);
    try {
      await hoxiAPI.abortSession(subID, sessionDirectory(subID));
    } catch (err) {
      console.error("abort subagent failed:", err);
    } finally {
      setStoppingID(null);
    }
  };

  const stopAllRunning = async () => {
    const running = subagents().filter((s) => s.status === "running");
    if (running.length === 0 || stoppingID()) return;
    setStoppingID("all");
    try {
      await Promise.all(
        running.map((s) =>
          hoxiAPI.abortSession(s.sessionID, sessionDirectory(s.sessionID)).catch(() => {})
        )
      );
    } finally {
      setStoppingID(null);
    }
  };

  return (
    <Show when={subagents().length > 0}>
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/85 px-3 py-2">
        <div class="flex items-center justify-between gap-2 text-[12px]">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            class="flex items-center gap-2 text-left min-w-0 flex-1 cursor-pointer"
          >
            <Show
              when={runningCount() > 0}
              fallback={<Bot size={13} class="text-[var(--accent-primary)] shrink-0" />}
            >
              <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                <Spinner class="w-3.5 h-3.5" />
              </span>
            </Show>

            <span class="font-medium text-[var(--text-primary)] shrink-0">
              {t("timeline.subagentStack")} ·{" "}
              {runningCount() > 0
                ? t("timeline.subagentsRunning", { count: runningCount() })
                : t("timeline.subagentsDone", { count: subagents().length })}
            </span>

            <Show when={attentionCount() > 0}>
              <span class="inline-flex items-center gap-1 rounded-full bg-[var(--warn-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--warn-text)]">
                <ShieldAlert size={11} />
                {t("timeline.subagentsWaiting", { count: attentionCount() })}
              </span>
            </Show>
          </button>

          <div class="flex items-center gap-1.5 shrink-0">
            <Show when={runningCount() > 1}>
              <button
                type="button"
                disabled={!!stoppingID()}
                onClick={() => void stopAllRunning()}
                class="inline-flex items-center gap-1 rounded-md bg-[var(--danger-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--danger-text)] hover:opacity-90 cursor-pointer"
              >
                <Square size={9} class="fill-current" />
                <span>{t("timeline.stopAllSubagents")}</span>
              </button>
            </Show>
            <button
              type="button"
              onClick={() => setExpanded((v) => !v)}
              class="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {expanded() ? <ChevronDown size={13} /> : <ChevronUp size={13} />}
            </button>
          </div>
        </div>

        <Show when={expanded()}>
          <div class="mt-2 space-y-1.5 border-t border-[var(--border-subtle)] pt-2 max-h-40 overflow-y-auto">
            <For each={subagents()}>
              {(sub) => (
                <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-panel)]/70 px-2.5 py-1.5 text-[11.5px]">
                  <div class="flex items-center gap-2 min-w-0">
                    <Show
                      when={sub.status === "running"}
                      fallback={
                        <CheckCircle2
                          size={12}
                          class={
                            sub.status === "error"
                              ? "text-[var(--danger-text)] shrink-0"
                              : "text-[var(--ok-text)] shrink-0"
                          }
                        />
                      }
                    >
                      <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                        <Spinner class="w-3 h-3" />
                      </span>
                    </Show>
                    <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)] shrink-0">
                      {sub.agentType}
                    </span>
                    <span class="truncate text-[var(--text-primary)]">
                      {sub.description}
                    </span>
                  </div>

                  <div class="flex items-center gap-1.5 shrink-0">
                    <Show when={sub.status === "running"}>
                      <button
                        type="button"
                        disabled={!!stoppingID()}
                        onClick={() => void stopSubagent(sub.sessionID)}
                        class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] cursor-pointer"
                        title={t("timeline.stopSubagentTooltip")}
                      >
                        <Show
                          when={stoppingID() !== sub.sessionID}
                          fallback={<LoaderCircle size={10} class="animate-spin" />}
                        >
                          <Square size={9} class="fill-current" />
                        </Show>
                      </button>
                    </Show>
                    <button
                      type="button"
                      onClick={() => void selectSession(sub.sessionID)}
                      class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] cursor-pointer"
                    >
                      <span>{t("timeline.openSubagent")}</span>
                      <ExternalLink size={10} />
                    </button>
                  </div>
                </div>
              )}
            </For>
          </div>
        </Show>
      </div>
    </Show>
  );
}
