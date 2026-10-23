// web/src/components/timeline/Composer.tsx
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import {
  ArrowUp,
  Check,
  Clock,
  FolderOpen,
  LoaderCircle,
  RefreshCw,
  Sparkles,
  Square,
  X,
} from "lucide-solid";
import {
  appSettings,
  clearReviewComments,
  consumeComposerDraftEdit,
  forkFromMessage,
  getComposerSkill,
  getComposerSkillNotice,
  isSessionBusy,
  loadSkillDetail,
  loadSkillsCatalog,
  selectComposerSkill,
  sessionDirectory,
  sessionRuntimeForID,
  setComposerSkillNotice,
  stopSessionTask,
  submitSessionPrompt,
  switchActiveSessionSandboxMode,
  switchWorkspace,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import ModelSelector from "../models/ModelSelector";
import ComposerShelf from "./ComposerShelf";
import SessionGoalBar from "./SessionGoalBar";
import {
  ComposerAttachmentChips,
  ComposerAutocompletePopover,
} from "./composer-subcomponents";
import { ComposerSandboxPill } from "./ComposerSandboxPill";
import CodeSearchModal from "../codeindex/CodeSearchModal";
import ConvoyModal from "../orchestration/ConvoyModal";
import ContextProgressPill from "./ContextProgressPill";
import ApprovalModePill from "./ApprovalModePill";
import ModeSwitcher from "./ModeSwitcher";
import ComposerToolsPopover from "./ComposerToolsPopover";
import {
  createComposerAutocomplete,
  createImageAttachments,
  createPasteCollapse,
  createPromptHistory,
  createSessionDraftSync,
  inspectPromptContextMentions,
  resolveContextMentionsForSend,
} from "./composer-hooks";
import { locale, t } from "../../i18n";
import { isComposerIMEEvent } from "./composer-keyboard";
import { hoxiAPI } from "../../services/api";
import type { CodeIndexResult } from "../../services/code-index-api";
import type { RecallResult } from "../../services/recall-api";

export default function Composer() {
  const [text, setText] = createSignal("");
  const [cursorOffset, setCursorOffset] = createSignal(0);
  const [sending, setSending] = createSignal(false);
  const [stopping, setStopping] = createSignal(false);
  const [forking, setForking] = createSignal(false);
  const [switchingSandbox, setSwitchingSandbox] = createSignal(false);
  const [enhancing, setEnhancing] = createSignal(false);
  const [enhanceError, setEnhanceError] = createSignal<string | null>(null);
  const [enhanceState, setEnhanceState] = createSignal<{ original: string; enhanced: string } | null>(null);
  const [codeSearchOpen, setCodeSearchOpen] = createSignal(false);
  const [convoyOpen, setConvoyOpen] = createSignal(false);
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
  createEffect(() => {
    sessionID();
    setEnhanceState(null);
    setEnhanceError(null);
  });
  const busy = createMemo(() => isSessionBusy(sessionID()));
  const selectedSkill = createMemo(() => getComposerSkill(sessionID()));
  const skillNotice = createMemo(() => getComposerSkillNotice(sessionID()));
  const openSkillsBrowser = (inspectKey?: string) => {
    const sid = sessionID();
    const dir = sessionDirectory(sid) || workspace.rootDir || "";
    const rt = sessionRuntimeForID(sid);
    setWorkspace({ inspectorOpen: true, inspectorTab: "skills" });
    void loadSkillsCatalog({ sessionID: sid, directory: dir, runtime: rt });
    if (inspectKey) {
      void loadSkillDetail(inspectKey, { sessionID: sid, directory: dir, runtime: rt });
    }
  };
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

  const insertMention = (reference: string) => {
    const current = text();
    const offset = Math.min(cursorOffset(), current.length);
    const prefix = current.slice(0, offset);
    const suffix = current.slice(offset);
    const separator = prefix.length > 0 && !/\s$/.test(prefix) ? " " : "";
    setText(`${prefix}${separator}${reference} ${suffix}`);
    setCursorOffset(offset + separator.length + reference.length + 1);
    requestAnimationFrame(() => {
      textareaRef?.focus();
      adjustHeight();
    });
  };

  const insertCodeReference = (result: CodeIndexResult) => insertMention(`@${result.path}`);
  const insertSessionReference = (result: RecallResult) => insertMention(`@session:${result.sessionID}`);

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
  const imgAttach = createImageAttachments(sessionID);
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
    if (draft.text) {
      setText((prev) => (prev.trim() ? `${prev}\n${draft.text}` : draft.text));
    }
    if (draft.attachments?.length) {
      imgAttach.restoreFromAttachments(draft.attachments);
    }
    if (draft.skillSelection) {
      selectComposerSkill(id, draft.skillSelection);
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

  const contextMentions = createMemo(() =>
    inspectPromptContextMentions(text(), sessionID())
  );

  const removeContextMention = (token: string) => {
    const escaped = token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    setText((prev) =>
      prev
        .replace(new RegExp(`(?:^|\\s)${escaped}(?=\\s|$)`, "g"), " ")
        .replace(/^\s+/, "")
    );
    requestAnimationFrame(adjustHeight);
  };

  const lastMessageID = createMemo(() => {
    const id = sessionID();
    if (!id) return undefined;
    const turns = workspace.messagesBySession[id];
    if (!turns || turns.length === 0) return undefined;
    return turns[turns.length - 1].info.id;
  });

  const canSend = () =>
    (text().trim().length > 0 ||
      Boolean(selectedSkill()) ||
      reviewComments().length > 0 ||
      imgAttach.images().length > 0 ||
      pasteCollapse.pastes().length > 0) &&
    imgAttach.pendingCount() === 0 &&
    !sending() &&
    !switchingSandbox() &&
    !choosingFolder();

  const buildPayloadWithReviews = async (rawInput: string, sid: string | null): Promise<string> => {
    const raw = await resolveContextMentionsForSend(rawInput, sid);
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
    const withPastes = pasteCollapse.expandIntoPrompt(text());
    const hasReviews = reviewComments().length > 0;
    const attachments = imgAttach.toPromptAttachments();
    const currentSkill = selectedSkill() ? { ...selectedSkill()! } : undefined;
    if ((!withPastes.trim() && !hasReviews && attachments.length === 0 && !currentSkill) || sending() || switchingSandbox()) return;
    setEnhanceState(null);
    setEnhanceError(null);
    const draftSessionID = sessionID();
    const draftDirectory = workspace.rootDir;
    const draft = {
      text: text(),
      images: imgAttach.images(),
      pastes: pasteCollapse.pastes(),
      skill: currentSkill,
    };

    if (text().trim()) {
      promptHistory.recordPrompt(text().trim());
    }
    setSending(true);
    setText("");
    selectComposerSkill(draftSessionID, null);
    setComposerSkillNotice(draftSessionID, null);
    pasteCollapse.clearPastes();
    imgAttach.clearImages();
    draftSync.clearCurrentDraft();
    ac.reset();
    if (textareaRef) {
      textareaRef.style.height = "48px";
    }
    try {
      const payload = await buildPayloadWithReviews(withPastes, draftSessionID);
      const id = await submitSessionPrompt(payload, draftSessionID, {
        directory: draftDirectory,
        attachments,
        skillSelection: currentSkill,
      });
      clearReviewComments(id);
    } catch (err) {
      console.error("send prompt failed:", err);
      const errMsg = err instanceof Error ? err.message : String(err || "");
      if (errMsg) {
        imgAttach.setNotice(errMsg);
      }
      if (workspace.rootDir === draftDirectory && (!draftSessionID || sessionID() === draftSessionID)) {
        if (!text()) setText(draft.text);
        if (draft.skill && !getComposerSkill(sessionID())) {
          selectComposerSkill(sessionID(), draft.skill);
        }
        imgAttach.replaceImages([...draft.images, ...imgAttach.images()]);
        pasteCollapse.replacePastes([...draft.pastes, ...pasteCollapse.pastes()]);
        requestAnimationFrame(adjustHeight);
      }
    } finally {
      setSending(false);
    }
  };

  const resolveEnhanceModel = () => {
    const small = workspace.globalConfig?.small_model;
    if (typeof small === "string" && small.includes("/")) {
      const index = small.indexOf("/");
      return { providerID: small.slice(0, index), modelID: small.slice(index + 1) };
    }
    const selected = workspace.selectedModel;
    if (selected?.providerID && selected?.modelID) return selected;
    return null;
  };

  const onEnhance = async () => {
    const original = enhanceState()?.original || text().trim();
    if (!original || enhancing()) return;
    const model = resolveEnhanceModel();
    if (!model) {
      setEnhanceError(t("timeline.enhanceNoModel"));
      return;
    }
    setEnhancing(true);
    setEnhanceError(null);
    try {
      const result = await hoxiAPI.enhancePrompt({
        providerID: model.providerID,
        modelID: model.modelID,
        prompt: original,
        locale: locale(),
      });
      const enhanced = result?.prompt?.trim();
      if (result?.ok && enhanced) {
        setEnhanceState({ original, enhanced });
      } else {
        setEnhanceError(result?.error || t("timeline.enhanceFailed"));
      }
    } catch (err) {
      setEnhanceError(err instanceof Error ? err.message : t("timeline.enhanceFailed"));
    } finally {
      setEnhancing(false);
    }
  };

  const acceptEnhance = () => {
    const state = enhanceState();
    if (!state) return;
    setText(state.enhanced);
    setCursorOffset(state.enhanced.length);
    setEnhanceState(null);
    setEnhanceError(null);
    requestAnimationFrame(() => {
      adjustHeight();
      textareaRef?.focus();
      textareaRef?.setSelectionRange(state.enhanced.length, state.enhanced.length);
    });
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
        <SessionGoalBar />
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
            contextMentions={contextMentions()}
            onRemoveMention={removeContextMention}
            pendingCount={imgAttach.pendingCount()}
            notice={imgAttach.notice()}
            onDismissNotice={() => imgAttach.clearNotice()}
            selectedSkill={selectedSkill()}
            skillNotice={skillNotice()}
            onDismissSkillNotice={() => setComposerSkillNotice(sessionID(), null)}
            onRemoveSkill={() => selectComposerSkill(sessionID(), null)}
            onInspectSkill={(sk) => openSkillsBrowser(sk.key)}
            onRemoveImage={(id) => imgAttach.removeImage(id)}
            onRemovePaste={(id) => pasteCollapse.removePaste(id)}
          />

          <input
            ref={fileInputRef}
            type="file"
            accept="image/*,.heic,.heif,.tiff,.tif,.bmp,.webp,.png,.jpg,.jpeg,.gif,.pdf,.xlsx,.xls,.ods,.csv,.tsv,.docx,.doc,.pptx,.ppt,.rtf,.txt,.md,.markdown,.json,.jsonl,.yaml,.yml,.toml,.xml,.log,.py,.go,.ts,.tsx,.js,.jsx,.rs,.java,.c,.cpp,.h,.sh,.sql,.html,.css"
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
              if (
                imgAttach.handleClipboardPaste(ev, (fallbackText) => {
                  setText((prev) => prev + fallbackText);
                  requestAnimationFrame(adjustHeight);
                })
              ) {
                return;
              }
              pasteCollapse.handleTextPaste(ev);
            }}
            onInput={(ev) => {
              promptHistory.resetHistoryCursor();
              setCursorOffset(ev.currentTarget.selectionStart ?? ev.currentTarget.value.length);
              setText(ev.currentTarget.value);
              if (enhanceState()) {
                setEnhanceState(null);
                setEnhanceError(null);
              }
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

          <Show when={enhanceState()}>
            {(state) => (
              <div class="mt-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/35 px-2.5 py-2">
                <div class="flex items-center gap-1.5">
                  <Sparkles size={12} class="text-[var(--accent-primary)]" />
                  <span class="text-[11px] font-semibold text-[var(--text-primary)]">
                    {t("timeline.enhancedPromptTitle")}
                  </span>
                  <div class="ml-auto flex items-center gap-1">
                    <button
                      type="button"
                      class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:opacity-40"
                      disabled={enhancing()}
                      onClick={() => void onEnhance()}
                    >
                      <RefreshCw size={10} class={enhancing() ? "animate-spin" : ""} />
                      {t("timeline.enhanceRegenerate")}
                    </button>
                    <button
                      type="button"
                      class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                      onClick={acceptEnhance}
                    >
                      <Check size={10} />
                      {t("timeline.enhanceAccept")}
                    </button>
                    <button
                      type="button"
                      class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                      title={t("timeline.enhanceDiscard")}
                      onClick={() => {
                        setEnhanceState(null);
                        setEnhanceError(null);
                      }}
                    >
                      <X size={11} />
                    </button>
                  </div>
                </div>
                <div class="mt-1.5 max-h-40 overflow-y-auto whitespace-pre-wrap text-[12px] leading-relaxed text-[var(--text-secondary)] selectable-text">
                  {state().enhanced}
                </div>
              </div>
            )}
          </Show>
          <Show when={enhanceError()}>
            <p class="mt-1.5 text-[11px] text-[var(--danger-text)]">{enhanceError()}</p>
          </Show>

          <div class="mt-2 flex items-center justify-between gap-2 flex-wrap">
            {/* Left Action Pills: Attach (+), Agent Mode, Model Selector, Git Dropdown */}
            <div class="flex items-center gap-1 flex-wrap">
              <ComposerToolsPopover
                workspaceReady={Boolean(workspace.rootDir)}
                skillActive={Boolean(selectedSkill())}
                sandboxMode={effectiveSandboxMode()}
                runtime={sessionRuntimeForID(sessionID())}
                canEnhance={Boolean(text().trim())}
                enhancing={enhancing()}
                onAttach={() => fileInputRef?.click()}
                onOpenSkills={() => openSkillsBrowser(selectedSkill()?.key)}
                onOpenCodeSearch={() => setCodeSearchOpen(true)}
                onOpenConvoy={() => setConvoyOpen(true)}
                onSwitchSandbox={() => void onToggleSandbox("worktree")}
                onEnhance={() => void onEnhance()}
              />

              <ModeSwitcher />
              <ModelSelector />

              <Show when={workspace.rootDir} fallback={
                <button type="button" onClick={() => void chooseFolder()} disabled={choosingFolder()}
                  title={t("timeline.selectFolderHint")}
                  class="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer">
                  <FolderOpen size={14} />{t("timeline.selectFolder")}
                </button>
              }>
              <Show when={effectiveSandboxMode() === "worktree"}>
                <ComposerSandboxPill
                  mode={effectiveSandboxMode()}
                  switching={switchingSandbox()}
                  busy={busy()}
                  sessionID={sessionID()}
                  directory={sessionDirectory(sessionID()) || workspace.rootDir}
                  forking={forking()}
                  onToggle={(m) => void onToggleSandbox(m)}
                  onFork={onFork}
                />
              </Show>
              </Show>
            </div>

            {/* Right Context Progress + Approval Mode + Send / Queue / Stop Buttons */}
            <div class="flex items-center gap-1.5">
              <ContextProgressPill />
              <ApprovalModePill runtime={sessionRuntimeForID(sessionID())} sandboxMode={effectiveSandboxMode()} />

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
      <CodeSearchModal
        open={codeSearchOpen()}
        directory={sessionDirectory(sessionID()) || workspace.rootDir}
        onClose={() => setCodeSearchOpen(false)}
        onSelectCode={insertCodeReference}
        onSelectSession={insertSessionReference}
      />
      <ConvoyModal
        open={convoyOpen()}
        directory={sessionDirectory(sessionID()) || workspace.rootDir}
        onClose={() => setConvoyOpen(false)}
      />
    </div>
  );
}
