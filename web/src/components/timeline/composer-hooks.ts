// web/src/components/timeline/composer-hooks.ts
import { type Accessor, createEffect, createSignal, type Setter } from "solid-js";
import { hoxiAPI, type CommandInfo, type PromptAttachment } from "../../services/api";
import { sessionDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";

export interface AutocompleteItem {
  type: "file" | "command" | "context";
  label: string;
  detail?: string;
  insertText: string;
}

const ACCEPTED_IMAGE_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

export interface ImageItem {
  id: string;
  filename: string;
  mime: string;
  dataUrl: string;
}

export interface CollapsedPasteItem {
  id: string;
  lines: number;
  chars: number;
  preview: string;
  content: string;
}

/**
 * Ported from kilocode useImageAttachments.ts:
 * Supports Cmd+V clipboard screenshot paste, drag-and-drop image files, and file picker.
 */
export function createImageAttachments() {
  const [images, setImages] = createSignal<ImageItem[]>([]);
  const [dragging, setDragging] = createSignal(false);

  const addFile = (file: File) => {
    if (!ACCEPTED_IMAGE_MIMES.has(file.type)) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result !== "string") return;
      setImages((prev) => [
        ...prev,
        {
          id: `img_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
          filename: file.name || "screenshot.png",
          mime: file.type,
          dataUrl: reader.result as string,
        },
      ]);
    };
    reader.readAsDataURL(file);
  };

  const removeImage = (id: string) => {
    setImages((prev) => prev.filter((item) => item.id !== id));
  };

  const clearImages = () => setImages([]);

  const handleClipboardPaste = (ev: ClipboardEvent): boolean => {
    const items = Array.from(ev.clipboardData?.items ?? []);
    const imageItems = items.filter(
      (item) => item.kind === "file" && ACCEPTED_IMAGE_MIMES.has(item.type)
    );
    if (imageItems.length === 0) return false;
    ev.preventDefault();
    for (const item of imageItems) {
      const file = item.getAsFile();
      if (file) addFile(file);
    }
    return true;
  };

  const handleDragOver = (ev: DragEvent) => {
    if (!ev.dataTransfer?.types.includes("Files")) return;
    ev.preventDefault();
    setDragging(true);
  };

  const handleDragLeave = () => {
    setDragging(false);
  };

  const handleDrop = (ev: DragEvent) => {
    setDragging(false);
    if (!ev.dataTransfer?.files?.length) return;
    ev.preventDefault();
    for (const file of Array.from(ev.dataTransfer.files)) {
      addFile(file);
    }
  };

  const toPromptAttachments = (): PromptAttachment[] =>
    images().map((img) => ({
      type: "file",
      mime: img.mime,
      filename: img.filename,
      url: img.dataUrl,
    }));

  return {
    images,
    dragging,
    addFile,
    removeImage,
    clearImages,
    handleClipboardPaste,
    handleDragOver,
    handleDragLeave,
    handleDrop,
    toPromptAttachments,
  };
}

/**
 * Ported from kilocode usePasteCollapse.ts:
 * Collapses large multi-line pastes (>= 10 lines or >= 600 chars) into compact chips
 * above the Composer so pasting stack traces or code doesn't flood the input box.
 */
export function createPasteCollapse() {
  const [pastes, setPastes] = createSignal<CollapsedPasteItem[]>([]);

  const handleTextPaste = (ev: ClipboardEvent): boolean => {
    const raw = ev.clipboardData?.getData("text/plain") ?? "";
    if (!raw) return false;
    const lineList = raw.split(/\r\n|\n|\r/);
    if (lineList.length < 10 && raw.length < 600) {
      return false;
    }
    ev.preventDefault();
    const firstNonEmpty =
      lineList.map((l) => l.trim()).find((l) => l.length > 0) || "Pasted text";
    setPastes((prev) => [
      ...prev,
      {
        id: `pst_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
        lines: lineList.length,
        chars: raw.length,
        preview: firstNonEmpty.slice(0, 48),
        content: raw,
      },
    ]);
    return true;
  };

  const removePaste = (id: string) => {
    setPastes((prev) => prev.filter((p) => p.id !== id));
  };

  const clearPastes = () => setPastes([]);

  const expandIntoPrompt = (typedText: string): string => {
    const blocks = pastes().map((p) => p.content.trim());
    if (blocks.length === 0) return typedText.trim();
    const joinedBlocks = blocks.join("\n\n");
    return typedText.trim()
      ? `${joinedBlocks}\n\n${typedText.trim()}`
      : joinedBlocks;
  };

  return {
    pastes,
    handleTextPaste,
    removePaste,
    clearPastes,
    expandIntoPrompt,
  };
}

const HISTORY_STORAGE_KEY = "hoxi-prompt-history:v1";
const MAX_HISTORY = 50;

function loadStoredHistory(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter((s) => typeof s === "string");
  } catch {
    /* ignore */
  }
  return [];
}

/**
 * Ported from kilocode usePromptHistory.ts:
 * Shell-style Up/Down arrow history navigation in Composer.
 */
export function createPromptHistory(sessionID: Accessor<string | null>) {
  const [entries, setEntries] = createSignal<string[]>(loadStoredHistory());
  const [cursorIdx, setCursorIdx] = createSignal(-1);
  let savedDraft = "";
  let lastSession: string | null = null;

  const syncSession = () => {
    const cur = sessionID();
    if (cur !== lastSession) {
      lastSession = cur;
      setCursorIdx(-1);
      savedDraft = "";
    }
  };

  const recordPrompt = (text: string) => {
    const cleaned = text.trim();
    if (!cleaned || cleaned.length > 4000) return;
    setCursorIdx(-1);
    savedDraft = "";
    setEntries((prev) => {
      const deduped = [cleaned, ...prev.filter((item) => item !== cleaned)].slice(
        0,
        MAX_HISTORY
      );
      try {
        localStorage.setItem(HISTORY_STORAGE_KEY, JSON.stringify(deduped));
      } catch {
        /* ignore */
      }
      return deduped;
    });
  };

  const navigateHistory = (
    direction: "up" | "down",
    currentText: string,
    selectionStart: number
  ): string | null => {
    syncSession();
    const list = entries();
    if (list.length === 0) return null;

    const browsing = cursorIdx() >= 0;
    if (direction === "up") {
      // Only trigger Up navigation when input is empty, already browsing, or cursor is on the first line
      const onFirstLine = !currentText.slice(0, selectionStart).includes("\n");
      if (!browsing && currentText.trim().length > 0 && !onFirstLine) return null;

      if (cursorIdx() === -1) {
        savedDraft = currentText;
        setCursorIdx(0);
        return list[0] ?? null;
      }
      const next = cursorIdx() + 1;
      if (next >= list.length) return null;
      setCursorIdx(next);
      return list[next] ?? null;
    }

    // direction === "down"
    if (!browsing) return null;
    const onLastLine = !currentText.slice(selectionStart).includes("\n");
    if (!onLastLine) return null;

    if (cursorIdx() > 0) {
      const next = cursorIdx() - 1;
      setCursorIdx(next);
      return list[next] ?? null;
    }
    setCursorIdx(-1);
    return savedDraft;
  };

  return {
    recordPrompt,
    navigateHistory,
    resetHistoryCursor: () => setCursorIdx(-1),
  };
}

/**
 * Ported from kilocode useGitChangesContext.ts, SessionMentionPicker.tsx & WorktreeMentionPicker.tsx:
 * Expands `@changes`, `@session:<id>`, and `@worktree:<id>` tokens into structured context blocks.
 */
export function expandContextMentions(rawPrompt: string, sessionID: string | null): string {
  const blocks: string[] = [];

  if (sessionID && rawPrompt.includes("@changes")) {
    const diffs = workspace.diffsBySession[sessionID] ?? [];
    if (diffs.length > 0) {
      const summary = diffs
        .slice(0, 15)
        .map((d) => `- \`${d.file}\` (+${d.additions} -${d.deletions})`)
        .join("\n");
      blocks.push(`<git_changes_context>\nCurrent session changed files:\n${summary}\n</git_changes_context>`);
    }
  }

  for (const match of rawPrompt.matchAll(/@session:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const targetSession = workspace.sessions.find((s) => s.id === refID);
    const turns = workspace.messagesBySession[refID] ?? [];
    const lastText = turns
      .slice(-2)
      .flatMap((turn) =>
        turn.parts
          .filter((p) => p.type === "text")
          .map((p) => `${turn.info.role.toUpperCase()}: ${(p as any).text.slice(0, 500)}`)
      )
      .join("\n");
    if (targetSession || lastText) {
      blocks.push(
        `<session_context id="${refID}" title="${targetSession?.title || refID}">\n${lastText || "Referenced session"}\n</session_context>`
      );
    }
  }

  for (const match of rawPrompt.matchAll(/@worktree:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const wt = workspace.worktreesBySession[refID];
    const diffs = workspace.diffsBySession[refID] ?? [];
    if (wt) {
      const diffLines = diffs
        .slice(0, 12)
        .map((d) => `- \`${d.file}\` (+${d.additions} -${d.deletions})`)
        .join("\n");
      blocks.push(
        `<worktree_context session="${refID}" branch="${wt.branch}" path="${wt.worktreePath}">\n${diffLines || "Isolated worktree branch"}\n</worktree_context>`
      );
    }
  }

  return blocks.length > 0 ? `${rawPrompt}\n\n${blocks.join("\n\n")}` : rawPrompt;
}

/**
 * Autocomplete hook for @file, @changes, @session, @worktree, and /command triggers in Composer.
 */
export function createComposerAutocomplete(
  text: Accessor<string>,
  setText: Setter<string>,
  sessionID: Accessor<string | null>,
  onApplied?: () => void
) {
  const [suggestions, setSuggestions] = createSignal<AutocompleteItem[]>([]);
  const [selectedIndex, setSelectedIndex] = createSignal(0);
  const [triggerMode, setTriggerMode] = createSignal<"file" | "command" | null>(null);
  const [cachedCommands, setCachedCommands] = createSignal<CommandInfo[] | null>(null);

  createEffect(() => {
    const val = text();
    const dir = sessionDirectory(sessionID());

    // 1. Check slash command at start: `/cmd`
    const cmdMatch = val.match(/^\/([^\s]*)$/);
    if (cmdMatch) {
      const query = cmdMatch[1].toLowerCase();
      setTriggerMode("command");
      void (async () => {
        let cmds = cachedCommands();
        if (!cmds) {
          try {
            cmds = await hoxiAPI.listCommands(dir);
            setCachedCommands(cmds);
          } catch {
            cmds = [];
          }
        }
        const filtered = (cmds ?? [])
          .filter(
            (c) =>
              c.name.toLowerCase().includes(query) ||
              (c.description ?? "").toLowerCase().includes(query)
          )
          .slice(0, 8)
          .map((c) => ({
            type: "command" as const,
            label: `/${c.name}`,
            detail: c.description || "Run command",
            insertText: `/${c.name} `,
          }));
        setSuggestions(filtered);
        setSelectedIndex(0);
      })();
      return;
    }

    // 2. Check @file, @changes, @session, or @worktree mention at end of input
    const fileMatch = val.match(/(?:^|\s)@([^\s@]*)$/);
    if (fileMatch) {
      const query = fileMatch[1];
      const qLower = query.trim().toLowerCase();
      setTriggerMode("file");
      const timer = setTimeout(async () => {
        try {
          let files: string[] = [];
          if (query.trim().length > 0) {
            files = await hoxiAPI.findFiles(query.trim(), dir);
          } else {
            const nodes = await hoxiAPI.listFiles("", dir);
            files = (nodes ?? []).map((n) => n.path);
          }
          const contextItems: AutocompleteItem[] = [];
          if ("changes".includes(qLower)) {
            contextItems.push({
              type: "context",
              label: "@changes",
              detail: t("timeline.mentionChangesDetail"),
              insertText: "@changes ",
            });
          }

          // Cross-Worktree mentions (@worktree:<id>)
          for (const [sid, wt] of Object.entries(workspace.worktreesBySession)) {
            if (sid === sessionID()) continue;
            if (!qLower || "worktree".includes(qLower) || wt.branch.toLowerCase().includes(qLower)) {
              contextItems.push({
                type: "context",
                label: `@worktree:${sid}`,
                detail: t("timeline.mentionWorktreeDetail", { branch: wt.branch }),
                insertText: `@worktree:${sid} `,
              });
            }
          }

          // Cross-Session mentions (@session:<id>)
          for (const s of workspace.sessions.slice(0, 5)) {
            if (s.id === sessionID()) continue;
            const title = s.title || s.id.slice(0, 8);
            if (!qLower || "session".includes(qLower) || title.toLowerCase().includes(qLower)) {
              contextItems.push({
                type: "context",
                label: `@session:${s.id}`,
                detail: t("timeline.mentionSessionDetail", { title }),
                insertText: `@session:${s.id} `,
              });
            }
          }

          setSuggestions([
            ...contextItems.slice(0, 4),
            ...(files ?? []).slice(0, 6).map((f) => ({
              type: "file" as const,
              label: f,
              detail: t("timeline.mentionWorkspaceFile"),
              insertText: `@${f} `,
            })),
          ]);
          setSelectedIndex(0);
        } catch {
          setSuggestions([]);
        }
      }, 90);
      return () => clearTimeout(timer);
    }

    setTriggerMode(null);
    setSuggestions([]);
  });

  const applySuggestion = (item: AutocompleteItem) => {
    const val = text();
    if (item.type === "command") {
      setText(item.insertText);
    } else {
      const next = val.replace(/(?:^|\s)@([^\s@]*)$/, (m) => {
        const prefix = m.startsWith(" ") ? " " : "";
        return `${prefix}${item.insertText}`;
      });
      setText(next);
    }
    setTriggerMode(null);
    setSuggestions([]);
    onApplied?.();
  };

  const reset = () => {
    setTriggerMode(null);
    setSuggestions([]);
  };

  return {
    suggestions,
    selectedIndex,
    setSelectedIndex,
    triggerMode,
    applySuggestion,
    reset,
  };
}

