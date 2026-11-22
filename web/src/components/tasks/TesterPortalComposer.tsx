// web/src/components/tasks/TesterPortalComposer.tsx
import { createEffect, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  ArrowUp,
  CheckCircle2,
  ImagePlus,
  LoaderCircle,
  MessageSquarePlus,
  RotateCcw,
  X,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  appSettings,
  formatIntakeError,
  openImagePreview,
  setPortalError,
  submitTesterNewTask,
  submitTesterTaskFeedback,
  uploadTesterAttachment,
  type IntakeAttachmentRef,
  type IntakeTaskStatus,
} from "../../store/workspace";
import { isComposerIMEEvent } from "../timeline/composer-keyboard";

function adjustTextareaHeight(el: HTMLTextAreaElement | undefined, minPx: number, maxPx: number) {
  if (!el) return;
  el.style.height = "auto";
  el.style.height = `${Math.min(maxPx, Math.max(minPx, el.scrollHeight))}px`;
}

function extractClipboardFiles(ev: ClipboardEvent): File[] {
  const items = ev.clipboardData?.items;
  if (!items) return [];
  const files: File[] = [];
  for (const item of Array.from(items)) {
    if (item.kind === "file") {
      const f = item.getAsFile();
      if (f) files.push(f);
    }
  }
  return files;
}

function PortalAttachmentChips(props: {
  items: IntakeAttachmentRef[];
  onRemove: (id: string) => void;
}) {
  return (
    <Show when={props.items.length > 0}>
      <div class="mb-2.5 flex flex-wrap gap-2">
        <For each={props.items}>
          {(att) => (
            <div class="group relative flex items-center overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]">
              <Show
                when={att.mime.startsWith("image/") && att.url}
                fallback={
                  <span class="max-w-[180px] truncate px-2.5 py-1.5 pr-6 font-mono text-[11px] text-[var(--text-secondary)]">
                    {att.filename}
                  </span>
                }
              >
                <button
                  type="button"
                  onClick={() => openImagePreview({ src: att.url!, alt: att.filename })}
                  class="block cursor-zoom-in"
                >
                  <img
                    src={att.url}
                    alt={att.filename}
                    class="h-14 w-20 object-cover"
                  />
                </button>
              </Show>
              <button
                type="button"
                onClick={() => props.onRemove(att.id)}
                class="absolute top-1 right-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/65 text-white hover:bg-black/85 cursor-pointer"
              >
                <X size={10} />
              </button>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}

export function TesterNewTaskComposer() {
  const [title, setTitle] = createSignal("");
  const [desc, setDesc] = createSignal("");
  const [steps, setSteps] = createSignal("");
  const [attachments, setAttachments] = createSignal<IntakeAttachmentRef[]>([]);
  const [uploading, setUploading] = createSignal(false);
  const [submitting, setSubmitting] = createSignal(false);
  const [dragging, setDragging] = createSignal(false);

  let fileInputRef: HTMLInputElement | undefined;
  let descRef: HTMLTextAreaElement | undefined;
  let stepsRef: HTMLTextAreaElement | undefined;
  let composing = false;

  const canSubmit = () =>
    !submitting() &&
    !uploading() &&
    Boolean(title().trim()) &&
    Boolean(desc().trim() || steps().trim());

  const uploadFiles = async (files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 0) return;
    setUploading(true);
    setPortalError(null);
    try {
      for (const file of list) {
        const uploaded = await uploadTesterAttachment(file);
        setAttachments((prev) => [...prev, uploaded].slice(0, 6));
      }
    } catch (err) {
      setPortalError(formatIntakeError(err, "Upload failed"));
    } finally {
      setUploading(false);
    }
  };

  const handlePaste = (ev: ClipboardEvent) => {
    const files = extractClipboardFiles(ev);
    if (files.length > 0) {
      ev.preventDefault();
      void uploadFiles(files);
    }
  };

  const handleDrop = (ev: DragEvent) => {
    ev.preventDefault();
    setDragging(false);
    if (ev.dataTransfer?.files?.length) {
      void uploadFiles(ev.dataTransfer.files);
    }
  };

  const doSubmit = async () => {
    if (!canSubmit()) return;
    setSubmitting(true);
    setPortalError(null);
    try {
      await submitTesterNewTask({
        title: title().trim(),
        description: desc().trim(),
        steps: steps().trim() || undefined,
        attachmentIds: attachments().map((a) => a.id),
      });
      setTitle("");
      setDesc("");
      setSteps("");
      setAttachments([]);
      if (descRef) descRef.style.height = "auto";
      if (stepsRef) stepsRef.style.height = "auto";
    } catch (err) {
      setPortalError(formatIntakeError(err, "Submit failed"));
    } finally {
      setSubmitting(false);
    }
  };

  const onFormKeyDown = (ev: KeyboardEvent) => {
    if (isComposerIMEEvent(ev, composing)) return;
    if ((ev.metaKey || ev.ctrlKey) && ev.key === "Enter") {
      ev.preventDefault();
      void doSubmit();
    }
  };

  return (
    <div class="my-auto w-full py-6">
      <div class="mx-auto w-full max-w-[var(--thread-max-width)] space-y-4">
        <div class="space-y-1 px-1">
          <h2 class="text-[16px] font-semibold tracking-tight text-[var(--text-primary)]">
            {t("tasks.portalFormHeading")}
          </h2>
          <p class="text-[12.5px] leading-relaxed text-[var(--text-muted)]">
            {t("tasks.portalFormNotice")}
          </p>
        </div>

        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            void doSubmit();
          }}
          onPaste={handlePaste}
          onKeyDown={onFormKeyDown}
          onDragOver={(ev) => {
            ev.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={handleDrop}
          class={`chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3.5 pb-3 ${
            dragging() ? "ring-2 ring-[var(--accent-primary)]" : ""
          }`}
        >
          <PortalAttachmentChips
            items={attachments()}
            onRemove={(id) => setAttachments((prev) => prev.filter((x) => x.id !== id))}
          />

          <input
            type="text"
            required
            value={title()}
            onInput={(ev) => setTitle(ev.currentTarget.value)}
            onCompositionStart={() => {
              composing = true;
            }}
            onCompositionEnd={() => {
              composing = false;
            }}
            placeholder={`${t("tasks.portalTitleLabel")}：${t("tasks.portalTitlePlaceholder")}`}
            class="w-full border-b border-[var(--border-subtle)] bg-transparent pb-2.5 text-[14px] font-medium text-[var(--text-primary)] placeholder:font-normal placeholder:text-[var(--text-faint)] outline-none selectable-text"
          />

          <textarea
            ref={descRef}
            rows={3}
            style={{ "font-size": "var(--app-chat-text-size)" }}
            value={desc()}
            onInput={(ev) => {
              setDesc(ev.currentTarget.value);
              adjustTextareaHeight(ev.currentTarget, 84, 220);
            }}
            onCompositionStart={() => {
              composing = true;
            }}
            onCompositionEnd={() => {
              composing = false;
            }}
            placeholder={t("tasks.portalDescPlaceholder")}
            class="mt-2.5 w-full min-h-[84px] max-h-[220px] resize-none bg-transparent leading-[1.65] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
          />

          <div class="mt-2 rounded-xl border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/70 px-3 py-2">
            <label class="mb-1 block text-[11px] font-medium text-[var(--text-muted)]">
              {t("tasks.portalStepsLabel")}
            </label>
            <textarea
              ref={stepsRef}
              rows={2}
              value={steps()}
              onInput={(ev) => {
                setSteps(ev.currentTarget.value);
                adjustTextareaHeight(ev.currentTarget, 44, 140);
              }}
              onCompositionStart={() => {
                composing = true;
              }}
              onCompositionEnd={() => {
                composing = false;
              }}
              placeholder={t("tasks.portalStepsPlaceholder")}
              class="w-full min-h-[44px] max-h-[140px] resize-none bg-transparent text-[12.5px] leading-[1.6] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            />
          </div>

          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,video/*,.log,.txt,.pdf,.md,.json,.csv"
            class="hidden"
            onChange={(ev) => {
              if (ev.currentTarget.files) {
                void uploadFiles(ev.currentTarget.files);
                ev.currentTarget.value = "";
              }
            }}
          />

          <div class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--border-subtle)]/60 pt-2.5">
            <div class="flex items-center gap-2.5">
              <button
                type="button"
                disabled={uploading()}
                onClick={() => fileInputRef?.click()}
                class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[12px] text-[var(--text-secondary)] hover:border-[var(--border-strong)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
              >
                <Show
                  when={!uploading()}
                  fallback={<LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />}
                >
                  <ImagePlus size={13.5} />
                </Show>
                <span>{uploading() ? t("tasks.portalUploading") : t("tasks.portalAttachBtn")}</span>
              </button>
              <span class="hidden sm:inline text-[11px] text-[var(--text-faint)]">
                {t("tasks.portalMarkdownHint")}
              </span>
            </div>

            <button
              type="submit"
              disabled={!canSubmit()}
              class="inline-flex items-center gap-1.5 rounded-full bg-[var(--accent-primary)] px-4 py-1.5 text-[12.5px] font-medium text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-35 disabled:cursor-not-allowed transition-all cursor-pointer"
            >
              <Show
                when={!submitting()}
                fallback={<LoaderCircle size={14} class="animate-spin" />}
              >
                <ArrowUp size={14} strokeWidth={2.4} />
              </Show>
              <span>{submitting() ? t("tasks.portalSubmittingBtn") : t("tasks.portalSubmitBtn")}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export function TesterFloatingFeedbackComposer(props: {
  taskId: string;
  status: IntakeTaskStatus;
  onHeightChange?: (heightPx: number) => void;
}) {
  const [note, setNote] = createSignal("");
  const [attachments, setAttachments] = createSignal<IntakeAttachmentRef[]>([]);
  const [uploading, setUploading] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [dragging, setDragging] = createSignal(false);

  let hostRef: HTMLDivElement | undefined;
  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;
  let composing = false;

  const isRetestStage = () =>
    props.status === "awaiting_retest" || props.status === "succeeded";

  const hasContent = () => Boolean(note().trim() || attachments().length > 0);

  // Clear draft note/attachments cleanly when switching between different tasks.
  createEffect((prevTaskId?: string) => {
    const currentId = props.taskId;
    if (prevTaskId !== undefined && prevTaskId !== currentId) {
      setNote("");
      setAttachments([]);
      if (textareaRef) textareaRef.style.height = "auto";
    }
    return currentId;
  });

  onMount(() => {
    if (!hostRef || typeof ResizeObserver === "undefined") return;
    const report = () => {
      if (hostRef) props.onHeightChange?.(Math.ceil(hostRef.getBoundingClientRect().height));
    };
    report();
    const ro = new ResizeObserver(report);
    ro.observe(hostRef);
    onCleanup(() => ro.disconnect());
  });

  const uploadFiles = async (files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 0) return;
    setUploading(true);
    setPortalError(null);
    try {
      for (const file of list) {
        const uploaded = await uploadTesterAttachment(file);
        setAttachments((prev) => [...prev, uploaded].slice(0, 6));
      }
    } catch (err) {
      setPortalError(formatIntakeError(err, "Upload failed"));
    } finally {
      setUploading(false);
    }
  };

  const sendFeedback = async (action: "pass" | "reopen" | "comment") => {
    if (busy() || uploading()) return;
    if ((action === "reopen" || action === "comment") && !hasContent()) return;
    setBusy(true);
    setPortalError(null);
    try {
      await submitTesterTaskFeedback({
        taskId: props.taskId,
        action,
        text: note().trim() || undefined,
        attachmentIds: attachments().map((a) => a.id),
      });
      setNote("");
      setAttachments([]);
      if (textareaRef) textareaRef.style.height = "auto";
    } catch (err) {
      setPortalError(formatIntakeError(err, "Feedback failed"));
    } finally {
      setBusy(false);
    }
  };

  const handlePaste = (ev: ClipboardEvent) => {
    const files = extractClipboardFiles(ev);
    if (files.length > 0) {
      ev.preventDefault();
      void uploadFiles(files);
    }
  };

  const handleDrop = (ev: DragEvent) => {
    ev.preventDefault();
    setDragging(false);
    if (ev.dataTransfer?.files?.length) {
      void uploadFiles(ev.dataTransfer.files);
    }
  };

  const handleKeyDown = (ev: KeyboardEvent) => {
    if (isComposerIMEEvent(ev, composing)) return;
    if (ev.key === "Enter") {
      const shouldSend =
        appSettings.sendShortcut === "mod-enter"
          ? ev.metaKey || ev.ctrlKey
          : !ev.shiftKey;
      if (shouldSend && hasContent()) {
        ev.preventDefault();
        void sendFeedback(isRetestStage() ? "reopen" : "comment");
      }
    }
  };

  return (
    <div
      ref={hostRef}
      data-portal-floating-composer="true"
      class="pointer-events-none absolute inset-x-0 bottom-0 z-20 bg-gradient-to-t from-[var(--bg-panel)] via-[var(--bg-panel)]/92 to-transparent px-4 sm:px-6 pt-8 pb-4"
    >
      <div class="pointer-events-auto mx-auto w-full max-w-[var(--thread-max-width)]">
        <div
          onDragOver={(ev) => {
            ev.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={handleDrop}
          class={`chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3 pb-2.5 ${
            dragging() ? "ring-2 ring-[var(--accent-primary)]" : ""
          }`}
        >
          <Show when={isRetestStage()}>
            <div class="mb-2 flex items-center gap-2 rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)]/70 px-3 py-1.5 text-[12px] font-medium text-[var(--ok-text)]">
              <CheckCircle2 size={13.5} class="shrink-0" />
              <span>{t("tasks.portalRetestPrompt")}</span>
            </div>
          </Show>

          <PortalAttachmentChips
            items={attachments()}
            onRemove={(id) => setAttachments((prev) => prev.filter((x) => x.id !== id))}
          />

          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,video/*,.log,.txt,.pdf,.md,.json,.csv"
            class="hidden"
            onChange={(ev) => {
              if (ev.currentTarget.files) {
                void uploadFiles(ev.currentTarget.files);
                ev.currentTarget.value = "";
              }
            }}
          />

          <textarea
            ref={textareaRef}
            rows={1}
            style={{ "font-size": "var(--app-chat-text-size)" }}
            value={note()}
            onPaste={handlePaste}
            onInput={(ev) => {
              setNote(ev.currentTarget.value);
              adjustTextareaHeight(ev.currentTarget, 44, 160);
            }}
            onKeyDown={handleKeyDown}
            onCompositionStart={() => {
              composing = true;
            }}
            onCompositionEnd={(ev) => {
              composing = false;
              setNote(ev.currentTarget.value);
            }}
            onBlur={() => {
              composing = false;
            }}
            placeholder={t("tasks.portalCommentPlaceholder")}
            class="w-full min-h-[44px] max-h-[160px] resize-none bg-transparent leading-[1.65] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
          />

          <div class="mt-2 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--border-subtle)]/60 pt-2">
            <div class="flex items-center gap-2.5">
              <button
                type="button"
                disabled={uploading()}
                onClick={() => fileInputRef?.click()}
                class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:border-[var(--border-strong)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
              >
                <Show
                  when={!uploading()}
                  fallback={<LoaderCircle size={12.5} class="animate-spin text-[var(--accent-primary)]" />}
                >
                  <ImagePlus size={13} />
                </Show>
                <span>{uploading() ? t("tasks.portalUploading") : t("tasks.portalAttachBtn")}</span>
              </button>
              <span class="hidden md:inline text-[11px] text-[var(--text-faint)]">
                {t("tasks.portalMarkdownHint")}
              </span>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <Show
                when={isRetestStage()}
                fallback={
                  <button
                    type="button"
                    disabled={busy() || uploading() || !hasContent()}
                    onClick={() => void sendFeedback("comment")}
                    class="inline-flex items-center gap-1.5 rounded-full bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-35 disabled:cursor-not-allowed transition-all cursor-pointer"
                  >
                    <Show
                      when={!busy()}
                      fallback={<LoaderCircle size={13} class="animate-spin" />}
                    >
                      <MessageSquarePlus size={13} />
                    </Show>
                    <span>{t("tasks.portalSendCommentBtn")}</span>
                  </button>
                }
              >
                <button
                  type="button"
                  disabled={busy() || uploading() || !hasContent()}
                  onClick={() => void sendFeedback("reopen")}
                  class="inline-flex items-center gap-1 rounded-full border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 disabled:opacity-35 disabled:cursor-not-allowed transition-opacity cursor-pointer"
                >
                  <RotateCcw size={12.5} />
                  <span>{t("tasks.portalSendReopenBtn")}</span>
                </button>
                <button
                  type="button"
                  disabled={busy() || uploading()}
                  onClick={() => void sendFeedback("pass")}
                  class="inline-flex items-center gap-1 rounded-full bg-[var(--ok-surface)] px-3.5 py-1.5 text-[12px] font-semibold text-[var(--ok-text)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
                >
                  <CheckCircle2 size={13.5} />
                  <span>{t("tasks.portalPassBtn")}</span>
                </button>
              </Show>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
