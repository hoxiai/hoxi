// web/src/components/timeline/Composer.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowUp,
  ClipboardPaste,
  GitFork,
  ImagePlus,
  LoaderCircle,
  Slash,
  Square,
  Swords,
  WandSparkles,
  X,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  clearReviewComments,
  forkFromMessage,
  isSessionBusy,
  recordUserAbortAction,
  sessionDirectory,
  setWorkspace,
  submitSessionPrompt,
  workspace,
} from "../../store/workspace";
import ModelSelector from "../models/ModelSelector";
import { FileIcon } from "../icons";
import ComposerShelf from "./ComposerShelf";
import ContextProgressPill from "./ContextProgressPill";
import ModeSwitcher from "./ModeSwitcher";
import {
  createComposerAutocomplete,
  createImageAttachments,
  createPasteCollapse,
  createPromptHistory,
  expandContextMentions,
} from "./composer-hooks";
import { t } from "../../i18n";

export default function Composer() {
  const [text, setText] = createSignal("");
  const [sending, setSending] = createSignal(false);
  const [stopping, setStopping] = createSignal(false);
  const [forking, setForking] = createSignal(false);

  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;

  const sessionID = () => workspace.activeSessionID;
  const busy = createMemo(() => isSessionBusy(sessionID()));

  const adjustHeight = () => {
    if (!textareaRef) return;
    textareaRef.style.height = "auto";
    textareaRef.style.height = `${Math.min(180, Math.max(48, textareaRef.scrollHeight))}px`;
  };

  // Kilo Composer Hooks: Autocomplete, Image attachments, Paste Collapse, Prompt History
  const ac = createComposerAutocomplete(text, setText, sessionID, () => {
    textareaRef?.focus();
    adjustHeight();
  });
  const imgAttach = createImageAttachments();
  const pasteCollapse = createPasteCollapse();
  const promptHistory = createPromptHistory(sessionID);

  const reviewComments = createMemo(() => {
    const id = sessionID();
    return id ? (workspace.reviewCommentsBySession[id] ?? []) : [];
  });

  const lastMessageID = createMemo(() => {
    const id = sessionID();
    if (!id) return undefined;
    const turns = workspace.messagesBySession[id];
    if (!turns || turns.length === 0) return undefined;
    return turns[turns.length - 1].info.id;
  });

  const canSend = () =>
    (text().trim().length > 0 ||
      reviewComments().length > 0 ||
      imgAttach.images().length > 0 ||
      pasteCollapse.pastes().length > 0) &&
    !sending();

  const buildPayloadWithReviews = (): string => {
    const withPastes = pasteCollapse.expandIntoPrompt(text());
    const raw = expandContextMentions(withPastes, sessionID());
    const comments = reviewComments();
    if (comments.length === 0) return raw;

    const reviewLines = comments.map((c) => {
      const loc = c.line ? `${c.file}:${c.line}` : c.file;
      return `- \`${loc}\`: ${c.body}`;
    });
    const header =
      "Please address the following inline code review comments:\n" +
      reviewLines.join("\n");
    return raw ? `${header}\n\n${raw}` : header;
  };

  const onSend = async () => {
    const payload = buildPayloadWithReviews();
    const attachments = imgAttach.toPromptAttachments();
    if ((!payload && attachments.length === 0) || sending()) return;

    if (text().trim()) {
      promptHistory.recordPrompt(text().trim());
    }
    setSending(true);
    setText("");
    pasteCollapse.clearPastes();
    imgAttach.clearImages();
    ac.reset();
    if (textareaRef) {
      textareaRef.style.height = "48px";
    }
    try {
      const id = await submitSessionPrompt(payload, sessionID(), { attachments });
      clearReviewComments(id);
    } catch (err) {
      console.error("send prompt failed:", err);
    } finally {
      setSending(false);
    }
  };

  const onStop = async () => {
    const id = sessionID();
    if (!id || stopping()) return;
    recordUserAbortAction(id);
    setStopping(true);
    try {
      await hoxiAPI.abortSession(id, sessionDirectory(id));
    } catch (err) {
      console.error("abort failed:", err);
    } finally {
      setStopping(false);
    }
  };

  const onFork = async () => {
    const id = sessionID();
    if (!id || forking()) return;
    setForking(true);
    try {
      await forkFromMessage(id, lastMessageID());
    } catch (err) {
      console.error("fork failed:", err);
    } finally {
      setForking(false);
    }
  };

  const onKeyDown = (ev: KeyboardEvent) => {
    const list = ac.suggestions();
    if (ac.triggerMode() && list.length > 0) {
      if (ev.key === "ArrowDown") {
        ev.preventDefault();
        ac.setSelectedIndex((i) => (i + 1) % list.length);
        return;
      }
      if (ev.key === "ArrowUp") {
        ev.preventDefault();
        ac.setSelectedIndex((i) => (i - 1 + list.length) % list.length);
        return;
      }
      if (ev.key === "Tab" || (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing)) {
        ev.preventDefault();
        const chosen = list[ac.selectedIndex()];
        if (chosen) ac.applySuggestion(chosen);
        return;
      }
      if (ev.key === "Escape") {
        ev.preventDefault();
        ac.reset();
        return;
      }
    }

    // Kilo PromptHistory Up/Down arrow navigation
    if (
      (ev.key === "ArrowUp" || ev.key === "ArrowDown") &&
      !ev.shiftKey &&
      !ev.metaKey &&
      !ev.ctrlKey &&
      !ev.isComposing
    ) {
      const dir = ev.key === "ArrowUp" ? "up" : "down";
      const next = promptHistory.navigateHistory(
        dir,
        text(),
        textareaRef?.selectionStart ?? 0
      );
      if (next !== null) {
        ev.preventDefault();
        setText(next);
        requestAnimationFrame(adjustHeight);
        return;
      }
    }

    if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
      ev.preventDefault();
      void onSend();
    }
  };

  return (
    <div class="shrink-0 w-full px-6 pb-4 pt-1">
      <div class="relative mx-auto w-full max-w-[62rem]">
        {/* Floating @file / @changes / /command Autocomplete Popover */}
        <Show when={ac.triggerMode() && ac.suggestions().length > 0}>
          <div class="absolute bottom-full left-0 mb-2 w-full max-w-[32rem] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
            <div class="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              {ac.triggerMode() === "command"
                ? t("timeline.slashCommandsHeader")
                : t("timeline.mentionFileOrContextHeader")}
            </div>
            <div class="max-h-56 overflow-y-auto space-y-0.5">
              <For each={ac.suggestions()}>
                {(item, idx) => (
                  <button
                    type="button"
                    class={`w-full flex items-center justify-between gap-3 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] transition-colors cursor-pointer ${
                      idx() === ac.selectedIndex()
                        ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                        : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                    }`}
                    onMouseEnter={() => ac.setSelectedIndex(idx())}
                    onClick={() => ac.applySuggestion(item)}
                  >
                    <span class="inline-flex items-center gap-2 min-w-0 font-mono truncate">
                      {item.type === "command" || item.type === "context" ? (
                        <Slash size={12} class="text-[var(--accent-primary)] shrink-0" />
                      ) : (
                        <FileIcon
                          node={{
                            path: item.label,
                            type: item.label.endsWith("/") ? "directory" : "file",
                          }}
                          class="w-3.5 h-3.5 shrink-0"
                        />
                      )}
                      <span class="truncate">{item.label}</span>
                    </span>
                    <Show when={item.detail}>
                      <span class="text-[11px] text-[var(--text-muted)] truncate max-w-[180px]">
                        {item.detail}
                      </span>
                    </Show>
                  </button>
                )}
              </For>
            </div>
          </div>
        </Show>

        {/* Docked Questions, Approvals, Plan Follow-ups + Contextual Shelf */}
        <ComposerShelf />

        {/* Main Glass Composer Box */}
        <div
          class={`chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3 pb-2.5 transition-colors ${
            imgAttach.dragging() ? "ring-2 ring-[var(--accent-primary)]" : ""
          }`}
          onDragOver={imgAttach.handleDragOver}
          onDragLeave={imgAttach.handleDragLeave}
          onDrop={imgAttach.handleDrop}
        >
          {/* Kilo Attachment & Collapsed Paste Chips Shelf */}
          <Show
            when={imgAttach.images().length > 0 || pasteCollapse.pastes().length > 0}
          >
            <div class="mb-2 flex flex-wrap items-center gap-1.5">
              <For each={imgAttach.images()}>
                {(img) => (
                  <div class="group relative flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-1 pr-2 py-1 text-[11px]">
                    <img
                      src={img.dataUrl}
                      alt={img.filename}
                      class="h-6 w-6 rounded object-cover"
                    />
                    <span class="max-w-[110px] truncate font-mono text-[var(--text-secondary)]">
                      {img.filename}
                    </span>
                    <button
                      type="button"
                      onClick={() => imgAttach.removeImage(img.id)}
                      class="text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                    >
                      <X size={11} />
                    </button>
                  </div>
                )}
              </For>

              <For each={pasteCollapse.pastes()}>
                {(p) => (
                  <div
                    class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)]/50 px-2.5 py-1 text-[11px] text-[var(--accent-text)]"
                    title={p.preview}
                  >
                    <ClipboardPaste size={11} class="shrink-0" />
                    <span class="font-mono font-medium">
                      {t("timeline.pastedLinesChip", { count: p.lines })}
                    </span>
                    <button
                      type="button"
                      onClick={() => pasteCollapse.removePaste(p.id)}
                      class="hover:text-[var(--danger-text)] cursor-pointer"
                    >
                      <X size={11} />
                    </button>
                  </div>
                )}
              </For>
            </div>
          </Show>

          <input
            ref={fileInputRef}
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            multiple
            class="hidden"
            onChange={(ev) => {
              const files = ev.currentTarget.files;
              if (files) {
                for (const f of Array.from(files)) imgAttach.addFile(f);
              }
              ev.currentTarget.value = "";
            }}
          />

          <textarea
            ref={textareaRef}
            class="w-full min-h-[48px] max-h-[180px] resize-none bg-transparent text-[14px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            rows={1}
            placeholder={
              busy()
                ? t("timeline.steerPlaceholder")
                : t("timeline.inputPlaceholder")
            }
            value={text()}
            onPaste={(ev) => {
              if (imgAttach.handleClipboardPaste(ev)) return;
              pasteCollapse.handleTextPaste(ev);
            }}
            onInput={(ev) => {
              promptHistory.resetHistoryCursor();
              setText(ev.currentTarget.value);
              adjustHeight();
            }}
            onKeyDown={onKeyDown}
          />

          <div class="mt-2 flex items-center justify-between gap-2 flex-wrap">
            {/* Left Action Pills: Agent Mode, Model Selector, Image Attach, Arena, Fork, Context Progress */}
            <div class="flex items-center gap-1.5 flex-wrap">
              <ModeSwitcher />
              <ModelSelector />

              <button
                type="button"
                onClick={() => fileInputRef?.click()}
                class="inline-flex items-center gap-1 rounded-full px-2 py-1 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                title={t("timeline.attachImageTooltip")}
              >
                <ImagePlus size={13} />
              </button>

              <button
                type="button"
                onClick={() => setWorkspace("arenaModalOpen", true)}
                class="inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                title={t("timeline.arenaTooltip")}
              >
                <Swords size={12} />
                <span class="hidden sm:inline">{t("timeline.arenaBtn")}</span>
              </button>

              <Show when={sessionID()}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 transition-colors cursor-pointer"
                  disabled={forking()}
                  onClick={onFork}
                  title={t("timeline.forkConfirm")}
                >
                  <Show
                    when={!forking()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <GitFork size={12} />
                  </Show>
                  {t("timeline.forkHere")}
                </button>
              </Show>

              <ContextProgressPill />
            </div>

            {/* Right Send / Stop / Steer Actions */}
            <div class="flex items-center gap-2">
              <Show when={busy()}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-full border border-[var(--violet-border)] bg-[var(--violet-surface)] px-3 py-1 text-[11.5px] font-medium text-[var(--violet-text)] disabled:opacity-40 transition-all cursor-pointer"
                  disabled={!canSend()}
                  onClick={onSend}
                  title={t("timeline.steer")}
                >
                  <Show
                    when={!sending()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <WandSparkles size={12} />
                  </Show>
                  {t("timeline.steer")}
                </button>

                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--status-danger)] text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={stopping()}
                  onClick={onStop}
                  title={t("timeline.stop")}
                >
                  <Show
                    when={!stopping()}
                    fallback={<LoaderCircle size={13} class="animate-spin" />}
                  >
                    <Square size={11} class="fill-current" />
                  </Show>
                </button>
              </Show>

              <Show when={!busy()}>
                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent-primary)] text-white shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
                  disabled={!canSend()}
                  onClick={onSend}
                  title={`${t("timeline.send")} (Enter)`}
                >
                  <Show
                    when={!sending()}
                    fallback={<LoaderCircle size={14} class="animate-spin" />}
                  >
                    <ArrowUp size={15} strokeWidth={2.5} />
                  </Show>
                </button>
              </Show>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
