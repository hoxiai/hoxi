// web/src/components/timeline/Composer.tsx
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import {
  ArrowUp,
  Clock,
  FolderOpen,
  LoaderCircle,
  Plus,
  Square,
} from "lucide-solid";
import {
  appSettings,
  clearReviewComments,
  consumeComposerDraftEdit,
  ensureAIConsent,
  forkFromMessage,
  isSessionBusy,
  stopSessionTask,
  submitSessionPrompt,
  switchActiveSessionSandboxMode,
  switchWorkspace,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import ModelSelector from "../models/ModelSelector";
import ComposerShelf from "./ComposerShelf";
import {
  ComposerAttachmentChips,
  ComposerAutocompletePopover,
} from "./composer-subcomponents";
import { ComposerSandboxPill } from "./ComposerSandboxPill";
import ContextProgressPill from "./ContextProgressPill";
import ApprovalModePill from "./ApprovalModePill";
import ModeSwitcher from "./ModeSwitcher";
import {
  createComposerAutocomplete,
  createImageAttachments,
  createPasteCollapse,
  createPromptHistory,
  createSessionDraftSync,
  expandContextMentions,
} from "./composer-hooks";
import { t } from "../../i18n";
import { isComposerIMEEvent } from "./composer-keyboard";
import { hoxiAPI } from "../../services/api";

export default function Composer() {
  const [text, setText] = createSignal("");
  const [cursorOffset, setCursorOffset] = createSignal(0);
  const [sending, setSending] = createSignal(false);
  const [stopping, setStopping] = createSignal(false);
  const [forking, setForking] = createSignal(false);
  const [switchingSandbox, setSwitchingSandbox] = createSignal(false);
  const [choosingFolder, setChoosingFolder] = createSignal(false);
  const chooseFolder = async () => {
    if (choosingFolder()) return;
    const request = workspace.newThreadRequest;
    const directory = workspace.rootDir;
    const id = workspace.activeSessionID;
    setChoosingFolder(true);
    try {
      const result = await hoxiAPI.openDirectoryDialog();
      if (result?.path && workspace.newThreadRequest === request
        && workspace.rootDir === directory && workspace.activeSessionID === id) {
        await switchWorkspace(result.path, false);
        requestAnimationFrame(() => textareaRef?.focus());
      }
    } catch (error) {
      setWorkspace("sessionSetupError", String(error));
    } finally {
      setChoosingFolder(false);
    }
  };

  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;
  let composing = false;

  const sessionID = () => workspace.activeSessionID;
  const busy = createMemo(() => isSessionBusy(sessionID()));
  const effectiveSandboxMode = createMemo<"local" | "worktree">(() => {
    const id = sessionID();
    if (id && workspace.worktreeBySession[id]) {
      return workspace.worktreeBySession[id].isolated ? "worktree" : "local";
    }
    return appSettings.defaultSandboxMode;
  });

  const onToggleSandbox = async (mode: "local" | "worktree") => {
    if (switchingSandbox() || busy()) return;
    if (effectiveSandboxMode() === mode) return;
    setSwitchingSandbox(true);
    try {
      await switchActiveSessionSandboxMode(mode);
    } catch (err) {
      console.error("switch session sandbox failed:", err);
    } finally {
      setSwitchingSandbox(false);
    }
  };

  const adjustHeight = () => {
    if (!textareaRef) return;
    textareaRef.style.height = "auto";
    textareaRef.style.height = `${Math.min(180, Math.max(48, textareaRef.scrollHeight))}px`;
  };

  // Kilo Composer Hooks: Autocomplete, Image attachments, Paste Collapse, Prompt History
  const ac = createComposerAutocomplete(
    text,
    setText,
    sessionID,
    (nextCursor) => {
      textareaRef?.focus();
      if (typeof nextCursor === "number") {
        setCursorOffset(nextCursor);
        queueMicrotask(() => {
          textareaRef?.setSelectionRange(nextCursor, nextCursor);
        });
      }
      adjustHeight();
    },
    cursorOffset
  );
  const imgAttach = createImageAttachments();
  const pasteCollapse = createPasteCollapse();
  const promptHistory = createPromptHistory(sessionID);
  const draftSync = createSessionDraftSync({
    sessionID,
    resetRequest: () => workspace.newThreadRequest,
    text,
    setText,
    imgAttach,
    pasteCollapse,
    onAfterRestore: () => {
      ac.reset();
      requestAnimationFrame(() => {
        adjustHeight();
        if (!sessionID()) textareaRef?.focus();
      });
    },
  });

  // Restore draft when user clicks "Edit" on a queued prompt or reverts a user message
  createEffect(() => {
    const id = sessionID();
    if (!id) return;
    const draft = workspace.composerDraftEditBySession[id];
    if (!draft) return;
    consumeComposerDraftEdit(id);
    setText((prev) => (prev.trim() ? `${prev}\n${draft.text}` : draft.text));
    if (draft.attachments?.length) {
      imgAttach.restoreFromAttachments(draft.attachments);
    }
    requestAnimationFrame(() => {
      adjustHeight();
      textareaRef?.focus();
    });
  });

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
    !sending() && !switchingSandbox() && !choosingFolder();

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
    if (choosingFolder()) return;
    if (!sessionID() && !workspace.rootDir) {
      await chooseFolder();
      return;
    }
    const payload = buildPayloadWithReviews();
    const attachments = imgAttach.toPromptAttachments();
    if ((!payload && attachments.length === 0) || sending() || switchingSandbox()) return;
    if (!ensureAIConsent()) return;
    const draftSessionID = sessionID();
    const draftDirectory = workspace.rootDir;
    const draft = { text: text(), images: imgAttach.images(), pastes: pasteCollapse.pastes() };

    if (text().trim()) {
      promptHistory.recordPrompt(text().trim());
    }
    setSending(true);
    setText("");
    pasteCollapse.clearPastes();
    imgAttach.clearImages();
    draftSync.clearCurrentDraft();
    ac.reset();
    if (textareaRef) {
      textareaRef.style.height = "48px";
    }
    try {
      const id = await submitSessionPrompt(payload, sessionID(), {
        attachments,
      });
      clearReviewComments(id);
    } catch (err) {
      console.error("send prompt failed:", err);
      if (workspace.rootDir === draftDirectory && (!draftSessionID || sessionID() === draftSessionID)) {
        if (!text()) setText(draft.text);
        imgAttach.replaceImages([...draft.images, ...imgAttach.images()]);
        pasteCollapse.replacePastes([...draft.pastes, ...pasteCollapse.pastes()]);
        requestAnimationFrame(adjustHeight);
      }
    } finally {
      setSending(false);
    }
  };

  const onStop = async () => {
    const id = sessionID();
    if (!id || stopping()) return;
    setStopping(true);
    try {
      await stopSessionTask(id);
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
    if (isComposerIMEEvent(ev, composing)) return;

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

    if (ev.key === "Escape" && busy()) {
      ev.preventDefault();
      void onStop();
      return;
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

    if (ev.key === "Enter" && !ev.isComposing) {
      const shouldSend =
        appSettings.sendShortcut === "mod-enter"
          ? ev.metaKey || ev.ctrlKey
          : !ev.shiftKey;
      if (shouldSend) {
        ev.preventDefault();
        void onSend();
      }
    }
  };

  return (
    <div class="my-auto shrink-0 w-full px-6 pb-4 pt-1">
      <div class="relative mx-auto w-full max-w-[var(--thread-max-width)]">
        {/* Floating @file / @changes / /command Autocomplete Popover */}
        <ComposerAutocompletePopover
          triggerMode={ac.triggerMode()}
          suggestions={ac.suggestions()}
          selectedIndex={ac.selectedIndex()}
          onSelectIndex={(idx) => ac.setSelectedIndex(idx)}
          onApply={(item) => ac.applySuggestion(item)}
        />

        {/* Docked Questions, Approvals, Plan Follow-ups + Contextual Shelf */}
        <ComposerShelf />

        {/* Main Glass Composer Box */}
        <div
          class={`chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3.5 pb-3 transition-colors ${
            imgAttach.dragging() ? "ring-2 ring-[var(--accent-primary)]" : ""
          }`}
          onDragOver={imgAttach.handleDragOver}
          onDragLeave={imgAttach.handleDragLeave}
          onDrop={imgAttach.handleDrop}
        >
          {/* Kilo Attachment & Collapsed Paste Chips Shelf */}
          <ComposerAttachmentChips
            images={imgAttach.images()}
            pastes={pasteCollapse.pastes()}
            onRemoveImage={(id) => imgAttach.removeImage(id)}
            onRemovePaste={(id) => pasteCollapse.removePaste(id)}
          />

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
            style={{ "font-size": "var(--app-chat-text-size)" }}
            class="w-full min-h-[48px] max-h-[180px] resize-none bg-transparent leading-[1.65] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            rows={1}
            placeholder={
              busy() ? t("timeline.queuePlaceholder") : t("timeline.inputPlaceholder")
            }
            value={text()}
            onPaste={(ev) => {
              if (imgAttach.handleClipboardPaste(ev)) return;
              pasteCollapse.handleTextPaste(ev);
            }}
            onInput={(ev) => {
              promptHistory.resetHistoryCursor();
              setCursorOffset(ev.currentTarget.selectionStart ?? ev.currentTarget.value.length);
              setText(ev.currentTarget.value);
              adjustHeight();
            }}
            onClick={(ev) => {
              setCursorOffset(ev.currentTarget.selectionStart ?? ev.currentTarget.value.length);
            }}
            onKeyUp={(ev) => {
              setCursorOffset(ev.currentTarget.selectionStart ?? ev.currentTarget.value.length);
            }}
            onSelect={(ev) => {
              setCursorOffset(ev.currentTarget.selectionStart ?? ev.currentTarget.value.length);
            }}
            onKeyDown={onKeyDown}
            onCompositionStart={() => { composing = true; }}
            onCompositionEnd={() => { composing = false; }}
            onBlur={() => { composing = false; }}
          />

          <div class="mt-2 flex items-center justify-between gap-2 flex-wrap">
            {/* Left Action Pills: Attach (+), Agent Mode, Model Selector, Git Dropdown */}
            <div class="flex items-center gap-1 flex-wrap">
              <button
                type="button"
                onClick={() => fileInputRef?.click()}
                class="inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                title={t("timeline.attachImageTooltip")}
              >
                <Plus size={15} />
              </button>

              <ModeSwitcher />
              <ModelSelector />

              <Show when={workspace.rootDir} fallback={
                <button type="button" onClick={() => void chooseFolder()} disabled={choosingFolder()}
                  title={t("timeline.selectFolderHint")}
                  class="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer">
                  <FolderOpen size={14} />{t("timeline.selectFolder")}
                </button>
              }>
              <ComposerSandboxPill
                mode={effectiveSandboxMode()}
                switching={switchingSandbox()}
                busy={busy()}
                sessionID={sessionID()}
                forking={forking()}
                onToggle={(m) => void onToggleSandbox(m)}
                onFork={onFork}
              />
              </Show>
            </div>

            {/* Right Context Progress + Approval Mode + Send / Queue / Stop Buttons */}
            <div class="flex items-center gap-1.5">
              <ContextProgressPill />
              <ApprovalModePill />

              <Show when={busy()}>
                <Show when={canSend()}>
                  <button
                    type="button"
                    class="inline-flex h-7 items-center justify-center gap-1 rounded-full bg-[var(--accent-primary)] px-2.5 text-[var(--accent-foreground)] shadow-xs hover:opacity-90 transition-all cursor-pointer"
                    onClick={onSend}
                    title={t("timeline.queueSend")}
                  >
                    <Show
                      when={!sending()}
                      fallback={<LoaderCircle size={14} class="animate-spin" />}
                    >
                      <Clock size={12.5} strokeWidth={2.2} />
                      <ArrowUp size={13.5} strokeWidth={2.5} />
                    </Show>
                  </button>
                </Show>

                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] disabled:opacity-50 transition-colors cursor-pointer"
                  disabled={stopping()}
                  onClick={onStop}
                  title={t("timeline.stopTooltip")}
                >
                  <Show
                    when={!stopping()}
                    fallback={<LoaderCircle size={13} class="animate-spin" />}
                  >
                    <Square size={10} class="fill-current" />
                  </Show>
                </button>
              </Show>

              <Show when={!busy()}>
                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
                  disabled={!canSend()}
                  onClick={onSend}
                  title={`${t("timeline.send")} (${
                    appSettings.sendShortcut === "mod-enter" ? "⌘/Ctrl+Enter" : "Enter"
                  })`}
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
        <Show when={!workspace.rootDir}>
          <p class="mt-2 px-1 text-xs text-[var(--text-faint)]">{t("timeline.selectFolderHint")}</p>
        </Show>
      </div>
    </div>
  );
}
