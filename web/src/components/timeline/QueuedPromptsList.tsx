// web/src/components/timeline/QueuedPromptsList.tsx
import { createMemo, For, Show } from "solid-js";
import { ArrowUp, Clock, Pencil, Trash2 } from "lucide-solid";
import {
  editQueuedPrompt,
  type QueuedPromptItem,
  removeQueuedPrompt,
  sendQueuedPromptNow,
  workspace,
} from "../../store/workspace";
import { TextShimmer } from "../icons";
import { t } from "../../i18n";

/**
 * Ported from Kilo Code `UserMessageDisplay` (`queued` state) & `session-queue.ts`:
 * Renders follow-up user prompts that were submitted while the session was busy.
 * Each queued message supports 3 immediate actions:
 *  - Send Now: injects the message before the next tool step without aborting
 *  - Edit: pops the message (and attachments) back into Composer for editing
 *  - Delete: removes the message from the queue
 */
export default function QueuedPromptsList(props: { sessionID: string | null }) {
  const queue = createMemo<QueuedPromptItem[]>(() => {
    const id = props.sessionID;
    return id ? workspace.queuedPromptsBySession[id] ?? [] : [];
  });

  return (
    <Show when={queue().length > 0 && props.sessionID}>
      {(sid) => (
        <div class="mt-5 flex flex-col gap-3">
          <For each={queue()}>
            {(item) => (
              <div
                data-queued-id={item.id}
                class="flex flex-col items-end rounded-xl transition-all"
              >
                <div class="max-w-[85%] rounded-2xl border border-dashed border-[var(--accent-border)] bg-[var(--bg-elevated)]/85 px-4 py-3 shadow-2xs space-y-2 opacity-90">
                  <Show when={item.attachments.length > 0}>
                    <div class="flex flex-wrap gap-2">
                      <For each={item.attachments}>
                        {(att) => (
                          <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] max-w-[200px]">
                            <img
                              src={att.url}
                              alt={att.filename || "attachment"}
                              class="max-h-36 w-auto object-contain block"
                            />
                          </div>
                        )}
                      </For>
                    </div>
                  </Show>

                  <Show when={item.text}>
                    <div
                      class="whitespace-pre-wrap break-words leading-[1.65] text-[var(--text-primary)] selectable-text"
                      style={{ "font-size": "var(--app-chat-text-size)" }}
                    >
                      {item.text}
                    </div>
                  </Show>
                </div>

                {/* Queued Status & Action Bar: Queued | Send Now | Edit | Delete */}
                <div class="mt-1.5 flex items-center justify-end gap-1.5 px-1 text-[12px]">
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11.5px] text-[var(--text-muted)]">
                    <Clock size={11} class="shrink-0" />
                    <TextShimmer text={t("timeline.queuedBadge")} active={true} />
                  </span>

                  <button
                    type="button"
                    onClick={() => void sendQueuedPromptNow(sid(), item.id)}
                    title={t("timeline.queueSendNowTooltip")}
                    class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--accent-primary)] transition-colors cursor-pointer"
                  >
                    <ArrowUp size={11} />
                    <span>{t("timeline.queueSendNow")}</span>
                  </button>

                  <button
                    type="button"
                    onClick={() => editQueuedPrompt(sid(), item.id)}
                    title={t("timeline.queueEditTooltip")}
                    class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <Pencil size={10.5} />
                    <span>{t("timeline.queueEdit")}</span>
                  </button>

                  <button
                    type="button"
                    onClick={() => removeQueuedPrompt(sid(), item.id)}
                    title={t("timeline.queueDeleteTooltip")}
                    class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--danger-text)] transition-colors cursor-pointer"
                  >
                    <Trash2 size={10.5} />
                    <span>{t("timeline.queueDelete")}</span>
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      )}
    </Show>
  );
}
