// web/src/components/timeline/composer-autocomplete.ts
import { type Accessor, createEffect, createSignal, onCleanup, type Setter } from "solid-js";
import { hoxiAPI, type CommandInfo } from "../../services/api";
import { sessionDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";

export interface AutocompleteItem {
  type: "file" | "command" | "context";
  label: string;
  detail?: string;
  insertText: string;
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
    const wt = workspace.worktreeBySession[refID];
    const diffs = workspace.diffsBySession[refID] ?? [];
    if (wt) {
      const diffLines = diffs
        .slice(0, 12)
        .map((d) => `- \`${d.file}\` (+${d.additions} -${d.deletions})`)
        .join("\n");
      blocks.push(
        `<worktree_context session="${refID}" branch="${wt.branch || refID}" path="${wt.directory}">\n${diffLines || "Isolated worktree branch"}\n</worktree_context>`
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
      let cancelled = false;
      const timer = setTimeout(async () => {
        try {
          let files: string[] = [];
          if (query.trim().length > 0) {
            files = await hoxiAPI.findFiles(query.trim(), dir);
          } else {
            const nodes = await hoxiAPI.listFiles("", dir);
            files = (nodes ?? []).map((n) => n.path);
          }
          if (cancelled) return;
          const contextItems: AutocompleteItem[] = [];
          if ("changes".includes(qLower)) {
            contextItems.push({
              type: "context",
              label: "@changes",
              detail: t("timeline.mentionChangesDetail"),
              insertText: "@changes ",
            });
          }

          for (const [sid, wt] of Object.entries(workspace.worktreeBySession)) {
            if (sid === sessionID()) continue;
            const branchName = wt.branch || sid;
            if (!qLower || "worktree".includes(qLower) || branchName.toLowerCase().includes(qLower)) {
              contextItems.push({
                type: "context",
                label: `@worktree:${sid}`,
                detail: t("timeline.mentionWorktreeDetail", { branch: branchName }),
                insertText: `@worktree:${sid} `,
              });
            }
          }

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
          if (!cancelled) setSuggestions([]);
        }
      }, 90);
      onCleanup(() => {
        cancelled = true;
        clearTimeout(timer);
      });
      return;
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
