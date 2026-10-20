// web/src/components/timeline/QueuedPromptsList.tsx
import { createMemo, For, Show } from "solid-js";
import { ArrowUp, Clock, Pencil, Sparkles, Trash2 } from "lucide-solid";
import {
  canSteerSession,
  editQueuedPrompt,
  type QueuedPromptItem,
  removeQueuedPrompt,
  sendQueuedPromptNow,
  workspace,
} from "../../store/workspace";
import { TextShimmer } from "../icons";
import { t } from "../../i18n";
import { ActionIconButton } from "./tool-card-helpers";

/**
 * Ported from Kilo Code `UserMessageDisplay` (`queued` state) & `session-queue.ts`:
 * Renders follow-up user prompts that were submitted while the session was busy.
 * Each queued message supports 3 immediate actions:
 *  - Send Now: injects the message before the next tool step without aborting
 *  - Edit: pops the message (and attachments) back into Composer for editing
 *  - Delete: removes the message from the queue
 */
export default function QueuedPromptsList(props: {
  sessionID: string | null;
  onSendNow: () => void;
}) {
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
                  <Show when={item.skillSelection}>
                    {(sk) => (
                      <div class="inline-flex items-center gap-1.5 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 font-mono text-[11px] font-medium text-[var(--accent-text)]">
                        <Sparkles size={11} class="shrink-0 text-[var(--accent-primary)]" />
                        <span>{t("skills.composerPillPrefix")}: {sk().name}</span>
                      </div>
                    )}
                  </Show>
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
                <div class="mt-1.5 flex items-center justify-end gap-1 px-1 text-[12px]">
                  <span class="mr-1 inline-flex items-center gap-1 px-1.5 py-0.5 text-[12px] text-[var(--text-muted)]">
                    <Clock size={11} class="shrink-0" />
                    <TextShimmer text={t("timeline.queuedBadge")} active={true} />
                  </span>

                  <ActionIconButton
                    disabled={!canSteerSession(sid())}
                    variant="accent"
                    label={
                      !canSteerSession(sid())
                        ? t("timeline.runnerFeatureUnsupported")
                        : t("timeline.queueSendNow")
                    }
                    onClick={() => {
                      props.onSendNow();
                      void sendQueuedPromptNow(sid(), item.id);
                    }}
                  >
                    <ArrowUp size={13} />
                  </ActionIconButton>

                  <ActionIconButton
                    label={t("timeline.queueEdit")}
                    onClick={() => editQueuedPrompt(sid(), item.id)}
                  >
                    <Pencil size={12} />
                  </ActionIconButton>

                  <ActionIconButton
                    variant="danger"
                    label={t("timeline.queueDelete")}
                    onClick={() => removeQueuedPrompt(sid(), item.id)}
                  >
                    <Trash2 size={12} />
                  </ActionIconButton>
                </div>
              </div>
            )}
          </For>
        </div>
      )}
    </Show>
  );
}
