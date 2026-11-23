import { createSignal, For, onMount, Show } from "solid-js";
import { deferredEntries, loadDeferred, resolveDeferred, workspace } from "../../store/workspace";
import { t } from "../../i18n";
import { SettingGroup } from "../settings/setting-controls";

export default function DeferredList() {
  const [copied, setCopied] = createSignal("");
  onMount(() => void loadDeferred());

  const sessionTitle = (sessionID: string) =>
    workspace.sessions.find((s) => s.id === sessionID)?.title || sessionID;

  const groups = () => {
    const map = new Map<string, ReturnType<typeof deferredEntries>>();
    for (const row of deferredEntries()) {
      const list = map.get(row.sessionID) ?? [];
      list.push(row);
      map.set(row.sessionID, list);
    }
    return Array.from(map.entries());
  };

  return (
    <Show when={groups().length > 0}>
      <SettingGroup title={t("approval.deferredTitle")}>
        <div class="divide-y divide-[var(--border-subtle)]/60">
          <For each={groups()}>
            {([sessionID, list]) => (
              <div class="p-4 space-y-2">
                <div class="text-[11.5px] font-medium text-[var(--text-muted)] truncate" title={sessionID}>
                  {sessionTitle(sessionID)}
                </div>
                <For each={list}>
                  {(row) => (
                    <div class="flex items-start justify-between gap-2 text-[12px]">
                      <div class="min-w-0 flex-1 space-y-0.5">
                        <div class="flex items-center gap-1.5">
                          <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                            {row.category}
                          </span>
                          <Show when={row.resolved}>
                            <span class="rounded bg-[var(--ok-surface)] px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--ok-text)]">
                              {t("approval.deferredResolved")}
                            </span>
                          </Show>
                        </div>
                        <code class="block truncate font-mono text-[11.5px] text-[var(--text-secondary)]">
                          {row.command}
                        </code>
                      </div>
                      <div class="flex shrink-0 gap-1.5">
                        <button
                          type="button"
                          class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                          onClick={() => {
                            void navigator.clipboard.writeText(row.command);
                            setCopied(row.id);
                          }}
                        >
                          {copied() === row.id ? t("approval.deferredCopied") : t("approval.deferredCopy")}
                        </button>
                        <Show when={!row.resolved}>
                          <button
                            type="button"
                            class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                            onClick={() => void resolveDeferred(row.id)}
                          >
                            {t("approval.deferredMark")}
                          </button>
                        </Show>
                      </div>
                    </div>
                  )}
                </For>
              </div>
            )}
          </For>
        </div>
      </SettingGroup>
    </Show>
  );
}
