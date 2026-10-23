import { createSignal, For, onMount, Show } from "solid-js";
import { deferredEntries, loadDeferred, resolveDeferred } from "../../store/workspace";
import { t } from "../../i18n";

export default function DeferredList() {
  const [copied, setCopied] = createSignal("");
  onMount(() => void loadDeferred());

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
    <div class="space-y-2">
      <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">{t("approval.deferredTitle")}</h4>
      <Show when={groups().length === 0}>
        <p class="text-[12px] text-[var(--text-muted)]">{t("approval.deferredEmpty")}</p>
      </Show>
      <For each={groups()}>
        {([sessionID, list]) => (
          <div class="rounded-xl border border-[var(--border-subtle)] p-3 space-y-2">
            <div class="text-[11px] text-[var(--text-faint)]">{sessionID}</div>
            <For each={list}>
              {(row) => (
                <div class="flex items-start justify-between gap-2 text-[12px]">
                  <div class="min-w-0">
                    <div class="text-[var(--text-muted)]">{row.category}</div>
                    <code class="block truncate text-[var(--text-secondary)]">{row.command}</code>
                  </div>
                  <div class="flex shrink-0 gap-1">
                    <button
                      type="button"
                      class="rounded border border-[var(--border-subtle)] px-2 py-0.5"
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
                        class="rounded border border-[var(--border-subtle)] px-2 py-0.5"
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
  );
}
