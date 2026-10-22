// web/src/components/settings/ApprovalAuditGrantsSection.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { RefreshCw, ShieldAlert, Trash2, X } from "lucide-solid";
import {
  approvalAuditEntries,
  approvalGrants,
  approvalRecordsLoading,
  clearApprovalAudit,
  grantClassLabel,
  loadApprovalAudit,
  loadApprovalGrants,
  revokeAllApprovalGrants,
  revokeApprovalGrant,
  type ApprovalAuditEntry,
  type ApprovalGrant,
} from "../../store/workspace";
import { t } from "../../i18n";
import { SettingGroup } from "./setting-controls";

function formatTime(ms: number): string {
  if (!ms) return "";
  try {
    return new Date(ms).toLocaleString(undefined, {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return "";
  }
}

function decisionLabel(decision: string): string {
  switch (decision) {
    case "once":
      return t("approval.decisionOnce");
    case "always":
      return t("approval.decisionAlways");
    case "reject":
      return t("approval.decisionReject");
    case "timeout":
      return t("approval.decisionTimeout");
    case "interrupted":
      return t("approval.decisionInterrupted");
    default:
      return decision;
  }
}

function decisionBadgeClass(decision: string): string {
  switch (decision) {
    case "once":
      return "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]";
    case "always":
      return "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]";
    case "reject":
    case "timeout":
    case "interrupted":
      return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)]";
  }
}

function sourceLabel(source: string): string {
  switch (source) {
    case "tunnel":
    case "lan":
    case "relay":
      return t("approval.sourceTunnel");
    case "system":
      return t("approval.sourceSystem");
    case "policy":
      return t("approval.sourcePolicy");
    case "grant":
      return t("approval.sourceGrant");
    default:
      return t("approval.sourceDesktop");
  }
}

function sourceBadgeClass(source: string): string {
  if (source === "tunnel" || source === "lan" || source === "relay") {
    return "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]";
  }
  return "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]";
}

export function ApprovalAuditGrantsSection() {
  const [busyID, setBusyID] = createSignal<string | null>(null);

  const refreshAll = async () => {
    await Promise.all([loadApprovalGrants(), loadApprovalAudit()]);
  };

  onMount(() => {
    void refreshAll();
  });

  const handleRevokeOne = async (grant: ApprovalGrant) => {
    if (busyID()) return;
    setBusyID(grant.id);
    try {
      await revokeApprovalGrant(grant.id);
    } finally {
      setBusyID(null);
    }
  };

  const handleRevokeAll = async () => {
    if (busyID()) return;
    setBusyID("all");
    try {
      await revokeAllApprovalGrants();
    } finally {
      setBusyID(null);
    }
  };

  const handleClearAudit = async () => {
    if (busyID()) return;
    setBusyID("clear-audit");
    try {
      await clearApprovalAudit();
    } finally {
      setBusyID(null);
    }
  };

  return (
    <div class="space-y-6">
      <SettingGroup
        title={t("approval.grantsSectionTitle")}
        tip={t("approval.grantsSectionDesc")}
        action={
          <div class="flex items-center gap-1.5">
            <Show when={approvalGrants().length > 0}>
              <button
                type="button"
                disabled={busyID() !== null}
                onClick={() => void handleRevokeAll()}
                class="inline-flex items-center gap-1 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer disabled:opacity-50"
              >
                <Trash2 size={12} />
                {t("approval.revokeAllGrants", { count: approvalGrants().length })}
              </button>
            </Show>
            <button
              type="button"
              disabled={approvalRecordsLoading()}
              onClick={() => void loadApprovalGrants()}
              class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
              title={t("approval.auditRefresh")}
            >
              <RefreshCw size={14} />
            </button>
          </div>
        }
      >
        <Show
          when={approvalGrants().length > 0}
          fallback={
            <div class="px-4 py-3 text-[12.5px] text-[var(--text-muted)]">
              {t("approval.grantsEmpty")}
            </div>
          }
        >
          <div class="divide-y divide-[var(--border-subtle)] max-h-[240px] overflow-y-auto">
            <For each={approvalGrants()}>
              {(grant) => (
                <div class="flex items-start justify-between gap-3 px-4 py-2.5 text-[12px]">
                  <div class="min-w-0 flex-1 space-y-1">
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="rounded border border-[var(--accent-border)] bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--accent-primary)]">
                        {grant.kind === "class" ? t("approval.grantKindClass") : grant.tool || grant.kind}
                      </span>
                      <span class="truncate font-medium text-[var(--text-primary)]">
                        {grant.sessionTitle || grant.sessionID}
                      </span>
                      <span class="text-[11px] text-[var(--text-muted)]">
                        {formatTime(grant.createdAt)}
                      </span>
                    </div>
                    <pre class="overflow-x-auto rounded bg-[var(--bg-inset)] px-2 py-1 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap break-all">
                      {grant.kind === "class" ? grantClassLabel(grant.pattern) : grant.pattern}
                    </pre>
                  </div>
                  <button
                    type="button"
                    disabled={busyID() !== null}
                    onClick={() => void handleRevokeOne(grant)}
                    class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] hover:border-[var(--danger-border)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer disabled:opacity-50"
                  >
                    <X size={12} />
                    {t("approval.revokeGrant")}
                  </button>
                </div>
              )}
            </For>
          </div>
        </Show>
      </SettingGroup>

      <SettingGroup
        title={t("approval.auditSectionTitle")}
        tip={t("approval.auditSectionDesc")}
        action={
          <div class="flex items-center gap-1.5">
            <Show when={approvalAuditEntries().length > 0}>
              <button
                type="button"
                disabled={busyID() !== null}
                onClick={() => void handleClearAudit()}
                class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer disabled:opacity-50"
              >
                <Trash2 size={12} />
                {t("approval.auditClear")}
              </button>
            </Show>
            <button
              type="button"
              disabled={approvalRecordsLoading()}
              onClick={() => void loadApprovalAudit()}
              class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
              title={t("approval.auditRefresh")}
            >
              <RefreshCw size={14} />
            </button>
          </div>
        }
      >
        <Show
          when={approvalAuditEntries().length > 0}
          fallback={
            <div class="px-4 py-3 text-[12.5px] text-[var(--text-muted)]">
              {t("approval.auditEmpty")}
            </div>
          }
        >
          <div class="divide-y divide-[var(--border-subtle)] max-h-[300px] overflow-y-auto">
            <For each={approvalAuditEntries()}>
              {(entry: ApprovalAuditEntry) => (
                <div class="px-4 py-2.5 space-y-1 text-[12px]">
                  <div class="flex flex-wrap items-center gap-1.5">
                    <span
                      class={`rounded border px-1.5 py-0.5 text-[10.5px] font-medium ${decisionBadgeClass(entry.decision)}`}
                    >
                      {decisionLabel(entry.decision)}
                    </span>
                    <span
                      class={`rounded border px-1.5 py-0.5 text-[10.5px] ${sourceBadgeClass(entry.source)}`}
                    >
                      {sourceLabel(entry.source)}
                    </span>
                    <Show when={entry.destructive}>
                      <span class="inline-flex items-center gap-1 rounded border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-0.5 text-[10.5px] font-semibold text-[var(--danger-text)]">
                        <ShieldAlert size={10} />
                        {t("approval.highRiskBadge")}
                      </span>
                    </Show>
                    <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                      {entry.tool || entry.permission}
                    </span>
                    <span class="truncate font-medium text-[var(--text-primary)]">
                      {entry.sessionTitle || entry.sessionID}
                    </span>
                    <span class="ml-auto text-[11px] text-[var(--text-muted)]">
                      {formatTime(entry.timestamp)}
                    </span>
                  </div>
                  <Show when={entry.summary}>
                    <pre class="overflow-x-auto rounded bg-[var(--bg-inset)] px-2 py-1 font-mono text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap break-all">
                      {entry.summary}
                    </pre>
                  </Show>
                  <Show when={entry.message}>
                    <div class="text-[11px] text-[var(--text-muted)]">
                      “{entry.message}”
                    </div>
                  </Show>
                </div>
              )}
            </For>
          </div>
        </Show>
      </SettingGroup>
    </div>
  );
}
