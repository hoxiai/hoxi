// web/src/components/timeline/QueuedPromptsList.tsx
import { createMemo, For, Show } from "solid-js";
import { ArrowUp, Clock, Pencil, Sparkles, Trash2 } from "lucide-solid";
import {
  canSteerSession,
  editQueuedPrompt,
  isSessionBusy,
  openAttachmentContextMenu,
  openAttachmentInInspector,
  openImagePreview,
  parseHoxiContextPrompt,
  type QueuedPromptItem,
  removeQueuedPrompt,
  sendQueuedPromptNow,
  workspace,
} from "../../store/workspace";
import { UserContextBlocks } from "./UserContextBlocks";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import {
  isAudioFilePart,
  isVideoFilePart,
  MediaAttachmentPlayer,
} from "./message-stream-parts";
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
                        {(att, idx) => {
                          const isImg =
                            (att.mime || "").toLowerCase().startsWith("image/") ||
                            /^data:image\//i.test(att.url || "") ||
                            /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i.test(
                              att.filename || ""
                            );
                          const isMedia =
                            !isImg && (isAudioFilePart(att) || isVideoFilePart(att));
                          return (
                            <Show
                              when={isImg}
                              fallback={
                                <Show
                                  when={isMedia}
                                  fallback={
                                    <a
                                      href={att.url}
                                      target="_blank"
                                      rel="noreferrer"
                                      download={att.filename || "attachment"}
                                      onContextMenu={(e) =>
                                        openAttachmentContextMenu(e, {
                                          kind: "file",
                                          src: att.url,
                                          filename: att.filename,
                                          mime: att.mime,
                                        })
                                      }
                                      onClick={(e) => {
                                        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                                        if (openAttachmentInInspector(att.url, att.filename)) {
                                          e.preventDefault();
                                        }
                                      }}
                                      class="inline-flex max-w-[240px] items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] hover:border-[var(--accent-border)] transition-colors"
                                    >
                                      <FileIcon
                                        node={{ path: att.filename || "file.txt", type: "file" }}
                                        class="h-4 w-4 shrink-0"
                                      />
                                      <span class="truncate font-mono text-[12px]">
                                        {att.filename || "attachment"}
                                      </span>
                                    </a>
                                  }
                                >
                                  <MediaAttachmentPlayer
                                    part={{
                                      id: `${item.id}_att_${idx()}`,
                                      sessionID: sid(),
                                      messageID: item.id,
                                      type: "file",
                                      mime: att.mime,
                                      filename: att.filename,
                                      url: att.url,
                                    }}
                                    compact
                                  />
                                </Show>
                              }
                            >
                              <button
                                type="button"
                                onClick={() =>
                                  openImagePreview({
                                    src: att.url,
                                    alt: att.filename || "attachment",
                                    filename: att.filename,
                                  })
                                }
                                onContextMenu={(e) =>
                                  openAttachmentContextMenu(e, {
                                    kind: "image",
                                    src: att.url,
                                    filename: att.filename,
                                    mime: att.mime,
                                    onPreview: () =>
                                      openImagePreview({
                                        src: att.url,
                                        alt: att.filename || "attachment",
                                        filename: att.filename,
                                      }),
                                  })
                                }
                                title={t("timeline.previewImageTooltip")}
                                class="block max-w-[200px] overflow-hidden rounded-lg border border-[var(--border-subtle)] cursor-zoom-in transition-colors hover:border-[var(--accent-border)]"
                              >
                                <img
                                  src={att.url}
                                  alt={att.filename || "attachment"}
                                  class="max-h-36 w-auto object-contain block"
                                />
                              </button>
                            </Show>
                          );
                        }}
                      </For>
                    </div>
                  </Show>

                  <Show when={item.text}>
                    {(() => {
                      const parsed = parseHoxiContextPrompt(item.text);
                      return (
                        <>
                          <Show when={parsed.hasContext}>
                            <UserContextBlocks
                              envelopes={parsed.envelopes}
                              partId={`queued_${item.id}`}
                            />
                          </Show>
                          <Show when={Boolean(parsed.bodyText)}>
                            <div
                              class="whitespace-pre-wrap break-words leading-[1.65] text-[var(--text-primary)] selectable-text"
                              style={{ "font-size": "var(--app-chat-text-size)" }}
                            >
                              {parsed.bodyText}
                            </div>
                          </Show>
                        </>
                      );
                    })()}
                  </Show>
                </div>

                {/* Queued Status & Action Bar: Queued | Send Now | Edit | Delete */}
                <div class="mt-1.5 flex items-center justify-end gap-1 px-1 text-[12px]">
                  <span
                    class="mr-1 inline-flex items-center px-1.5 py-0.5 text-[12px] text-[var(--text-muted)]"
                    title={t("timeline.queuedBadge")}
                  >
                    <Clock size={11} class="shrink-0" />
                  </span>

                  <ActionIconButton
                    disabled={isSessionBusy(sid()) && !canSteerSession(sid(), item.model)}
                    variant="accent"
                    label={
                      isSessionBusy(sid()) && !canSteerSession(sid(), item.model)
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
