// web/src/components/timeline/Composer.tsx
import { createMemo, createSignal, Show } from "solid-js";
import {
  ArrowUp,
  Folder,
  GitBranch,
  GitFork,
  ImagePlus,
  LoaderCircle,
  Square,
  Swords,
  WandSparkles,
} from "lucide-solid";
import {
  clearReviewComments,
  forkFromMessage,
  isSessionBusy,
  pauseSessionTask,
  resumePausedSession,
  setWorkspace,
  stopSessionTask,
  submitSessionPrompt,
  switchActiveSessionSandboxMode,
  workspace,
} from "../../store/workspace";
import ModelSelector from "../models/ModelSelector";
import ComposerShelf from "./ComposerShelf";
import {
  ComposerAttachmentChips,
  ComposerAutocompletePopover,
} from "./composer-subcomponents";
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
  const [switchingSandbox, setSwitchingSandbox] = createSignal(false);

  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;

  const sessionID = () => workspace.activeSessionID;
  const busy = createMemo(() => isSessionBusy(sessionID()));
  const isPaused = createMemo(() => {
    const id = sessionID();
    return id ? workspace.interruptedBySession[id] === "paused" : false;
  });
  const effectiveSandboxMode = createMemo<"local" | "worktree">(() => {
    const id = sessionID();
    if (id && workspace.worktreeBySession[id]) {
      return workspace.worktreeBySession[id].isolated ? "worktree" : "local";
    }
    return workspace.sessionSandboxMode;
  });

  const onToggleSandbox = async (mode: "local" | "worktree") => {
    if (switchingSandbox() || busy()) return;
    if (effectiveSandboxMode() === mode && workspace.sessionSandboxMode === mode) return;
    setSwitchingSandbox(true);
    try {
      await switchActiveSessionSandboxMode(mode);
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

  const onPause = async () => {
    const id = sessionID();
    if (!id || stopping()) return;
    setStopping(true);
    try {
      await pauseSessionTask(id);
    } finally {
      setStopping(false);
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

  const onResume = async () => {
    const id = sessionID();
    if (!id || sending()) return;
    const extra = buildPayloadWithReviews();
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
      await resumePausedSession(id, extra);
      clearReviewComments(id);
    } finally {
      setSending(false);
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

    if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
      ev.preventDefault();
      if (!busy() && isPaused() && !canSend()) {
        void onResume();
        return;
      }
      void onSend();
    }
  };

  return (
    <div class="shrink-0 w-full px-6 pb-4 pt-1">
      <div class="relative mx-auto w-full max-w-[62rem]">
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
          class={`chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3 pb-2.5 transition-colors ${
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
            class="w-full min-h-[48px] max-h-[180px] resize-none bg-transparent text-[14px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            rows={1}
            placeholder={
              busy()
                ? t("timeline.steerPlaceholder")
                : isPaused()
                  ? t("timeline.pausedPlaceholder")
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

              <div class="inline-flex items-center rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 text-[11px] font-medium select-none">
                <button
                  type="button"
                  disabled={switchingSandbox() || busy()}
                  onClick={() => void onToggleSandbox("local")}
                  class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 transition-all cursor-pointer disabled:opacity-50 ${
                    effectiveSandboxMode() === "local"
                      ? "bg-[var(--bg-hover)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  title={t("timeline.sandboxLocalTooltip")}
                >
                  <Folder size={11} class="shrink-0" />
                  <span>{t("timeline.sandboxLocalMode")}</span>
                </button>
                <button
                  type="button"
                  disabled={switchingSandbox() || busy()}
                  onClick={() => void onToggleSandbox("worktree")}
                  class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 transition-all cursor-pointer disabled:opacity-50 ${
                    effectiveSandboxMode() === "worktree"
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  title={t("timeline.sandboxWorktreeTooltip")}
                >
                  <Show
                    when={!switchingSandbox()}
                    fallback={<LoaderCircle size={11} class="animate-spin shrink-0" />}
                  >
                    <GitBranch size={11} class="shrink-0" />
                  </Show>
                  <span>{t("timeline.sandboxWorktreeMode")}</span>
                </button>
              </div>

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

            {/* Right Send / Pause / Stop / Resume / Steer Actions */}
            <div class="flex items-center gap-1.5">
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
                  class="inline-flex items-center gap-1 rounded-full border border-[var(--warn-border)] bg-[var(--warn-surface)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--warn-text)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={stopping()}
                  onClick={onPause}
                  title={t("timeline.pauseTooltip")}
                >
                  <span class="font-mono text-[10px] leading-none">⏸</span>
                  <span>{t("timeline.pause")}</span>
                </button>

                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-full bg-[var(--status-danger)] px-2.5 py-1 text-[11.5px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={stopping()}
                  onClick={onStop}
                  title={t("timeline.stopTooltip")}
                >
                  <Show
                    when={!stopping()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <Square size={10} class="fill-current" />
                  </Show>
                  <span>{t("timeline.stop")}</span>
                </button>
              </Show>

              <Show when={!busy()}>
                <Show when={isPaused()}>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1.5 rounded-full border border-[var(--ok-border)] bg-[var(--ok-surface)] px-3 py-1 text-[11.5px] font-medium text-[var(--ok-text)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                    disabled={sending()}
                    onClick={onResume}
                    title={t("timeline.resumeTooltip")}
                  >
                    <span class="text-[10px] leading-none">▶</span>
                    <span>
                      {canSend() ? t("timeline.resumeWithNote") : t("timeline.resume")}
                    </span>
                  </button>
                </Show>

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
