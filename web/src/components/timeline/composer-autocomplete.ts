// web/src/components/timeline/composer-autocomplete.ts
import { type Accessor, createEffect, createSignal, onCleanup, type Setter } from "solid-js";
import {
  hoxiAPI,
  skillsAPI,
  type CommandInfo,
  type SkillDescriptor,
  type SkillSelection,
} from "../../services/api";
import {
  defaultWorkspaceDir,
  expandContextMentions,
  inspectPromptContextMentions,
  isSkillHidden,
  recordSkillDescriptors,
  resolveContextMentionsForSend,
  selectComposerSkill,
  sessionDirectory,
  sessionRuntimeForID,
  setComposerSkillNotice,
  skillsCatalogRevision,
  skillsStore,
  workspace,
  type ContextMentionChipInfo,
} from "../../store/workspace";
import { t } from "../../i18n";

export {
  expandContextMentions,
  inspectPromptContextMentions,
  resolveContextMentionsForSend,
  type ContextMentionChipInfo,
};

export interface AutocompleteItem {
  type: "file" | "command" | "context";
  label: string;
  detail?: string;
  insertText: string;
  source?: "command" | "mcp" | "skill";
  hints?: string[];
  skillSelection?: SkillSelection;
}

export function buildSlashCommandSuggestions(params: {
  commands: CommandInfo[];
  skills: SkillDescriptor[];
  query: string;
  runtime: string;
  isHidden?: (key: string) => boolean;
}): AutocompleteItem[] {
  const q = params.query.trim().toLowerCase();
  const checkHidden = params.isHidden ?? isSkillHidden;
  const items: AutocompleteItem[] = [];
  const seenSkills = new Set<string>();

  for (const sk of params.skills) {
    if (checkHidden(sk.key) || checkHidden(sk.name)) continue;
    seenSkills.add(sk.name);
    if (
      !q ||
      sk.name.toLowerCase().includes(q) ||
      (sk.description ?? "").toLowerCase().includes(q)
    ) {
      items.push({
        type: "command",
        source: "skill",
        label: `/${sk.name}`,
        detail: sk.description || t("skills.groupSkills"),
        insertText: "",
        skillSelection: { key: sk.key, name: sk.name },
      });
    }
  }

  for (const c of params.commands) {
    const src = c.source === "skill" || c.source === "mcp" ? c.source : "command";
    if (src === "skill") {
      // Skills are surfaced from the verified catalog above so they always carry a selection key.
      continue;
    }
    if (seenSkills.has(c.name)) continue;
    if (
      !q ||
      c.name.toLowerCase().includes(q) ||
      (c.description ?? "").toLowerCase().includes(q)
    ) {
      items.push({
        type: "command",
        source: src,
        label: `/${c.name}`,
        detail: c.description || "Run command",
        insertText: `/${c.name} `,
        hints: c.hints?.length ? c.hints : undefined,
      });
    }
  }

  // The popover is scrollable, so keep a generous cap instead of hiding the
  // majority of the skill catalog once the list grows past a dozen entries.
  return items.slice(0, MAX_SLASH_SUGGESTIONS);
}

export interface AutocompleteToken {
  mode: "file" | "command";
  query: string;
  start: number;
  end: number;
}

/**
 * Resolves the active `/command` (or `/skill`) or `@mention` token at `cursorOffset` (defaults to end of `val`).
 * Supports triggering `/` and `@` at any position in the prompt (start, after whitespace, after CJK/punctuation, or mid-sentence).
 */
export function findAutocompleteToken(
  val: string,
  cursorOffset?: number
): AutocompleteToken | null {
  const cursor =
    typeof cursorOffset === "number"
      ? Math.max(0, Math.min(val.length, cursorOffset))
      : val.length;
  const before = val.slice(0, cursor);
  const after = val.slice(cursor);

  // Do not trigger when the cursor is in the middle of an ASCII word or path segment
  if (after.length > 0 && /^[a-zA-Z0-9_/@.-]/.test(after)) {
    return null;
  }

  // 1. Check @file, @changes, @session, or @worktree mention before cursor
  const fileMatch = before.match(/@([^\s@]*)$/);
  if (fileMatch) {
    const query = fileMatch[1];
    const start = cursor - query.length - 1;
    if (start >= 0 && val[start] === "@") {
      return {
        mode: "file",
        query,
        start,
        end: cursor,
      };
    }
  }

  // 2. Check /skill or /command trigger anywhere in the input
  const cmdMatch = before.match(/\/([^\s/@]*)$/);
  if (cmdMatch) {
    const query = cmdMatch[1];
    const start = cursor - query.length - 1;
    if (start >= 0 && val[start] === "/") {
      const prefixToken = before.slice(0, start).match(/[^\s]*$/)?.[0] ?? "";
      const isUrlScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:$/.test(prefixToken);
      const isDotPath = /^\.{1,2}$/.test(prefixToken);
      if (!/[/\\@]/.test(prefixToken) && !isUrlScheme && !isDotPath) {
        return {
          mode: "command",
          query,
          start,
          end: cursor,
        };
      }
    }
  }

  return null;
}

export function applyAutocompleteToken(
  val: string,
  token: AutocompleteToken,
  insertText: string
): { text: string; cursor: number } {
  const before = val.slice(0, token.start);
  const after = val.slice(token.end);
  const normalizedInsert =
    after.startsWith(" ") && insertText.endsWith(" ")
      ? insertText.slice(0, -1)
      : insertText;
  const nextText = `${before}${normalizedInsert}${after}`;
  return {
    text: nextText,
    cursor: before.length + insertText.length,
  };
}

const MAX_SLASH_SUGGESTIONS = 100;
const FILE_SEARCH_CACHE_TTL_MS = 15_000;
const COMMAND_CACHE_TTL_MS = 30_000;
const MAX_FILE_SEARCH_CACHE = 48;

const fileSearchCache = new Map<string, { files: string[]; expiresAt: number }>();
const commandByDirCache = new Map<string, { commands: CommandInfo[]; expiresAt: number }>();
const skillsByDirCache = new Map<string, { skills: SkillDescriptor[]; expiresAt: number }>();

export function invalidateComposerSkillsAutocompleteCache() {
  skillsByDirCache.clear();
}

function readCachedFileSearch(dir: string | undefined, query: string): string[] | null {
  const key = `${dir || "__root__"}\x00${query}`;
  const entry = fileSearchCache.get(key);
  if (!entry) return null;
  if (Date.now() > entry.expiresAt) {
    fileSearchCache.delete(key);
    return null;
  }
  fileSearchCache.delete(key);
  fileSearchCache.set(key, entry);
  return entry.files;
}

function writeCachedFileSearch(dir: string | undefined, query: string, files: string[]) {
  const key = `${dir || "__root__"}\x00${query}`;
  if (fileSearchCache.has(key)) fileSearchCache.delete(key);
  while (fileSearchCache.size >= MAX_FILE_SEARCH_CACHE) {
    const oldest = fileSearchCache.keys().next().value;
    if (oldest === undefined) break;
    fileSearchCache.delete(oldest);
  }
  fileSearchCache.set(key, {
    files,
    expiresAt: Date.now() + FILE_SEARCH_CACHE_TTL_MS,
  });
}

/**
 * Autocomplete hook for @file, @changes, @session, @worktree, and /command triggers in Composer.
 */
export function createComposerAutocomplete(
  text: Accessor<string>,
  setText: Setter<string>,
  sessionID: Accessor<string | null>,
  onApplied?: (nextCursor?: number) => void,
  cursorOffset?: Accessor<number>
) {
  const [suggestions, setSuggestions] = createSignal<AutocompleteItem[]>([]);
  const [selectedIndex, setSelectedIndex] = createSignal(0);
  const [triggerMode, setTriggerMode] = createSignal<"file" | "command" | null>(null);
  const [activeToken, setActiveToken] = createSignal<AutocompleteToken | null>(null);
  let requestGen = 0;
  onCleanup(() => {
    requestGen++;
  });

  createEffect(() => {
    const val = text();
    const cursor = cursorOffset ? cursorOffset() : val.length;
    const sid = sessionID();
    const dir =
      (sid ? sessionDirectory(sid) : workspace.rootDir) ||
      workspace.rootDir ||
      defaultWorkspaceDir() ||
      undefined;
    const token = findAutocompleteToken(val, cursor);
    setActiveToken(token);

    if (!token) {
      requestGen++;
      setTriggerMode(null);
      setSuggestions([]);
      return;
    }

    // 1. Slash command or skill: `/cmd`
    if (token.mode === "command") {
      const query = token.query.toLowerCase();
      const runtime = sessionRuntimeForID(sid);
      const rev = skillsCatalogRevision();
      const effectiveDir = dir || workspace.rootDir || defaultWorkspaceDir() || undefined;
      const dirKey = `${runtime}\x00${rev}\x00${effectiveDir || "__root__"}`;
      const reqID = ++requestGen;
      setTriggerMode("command");
      void (async () => {
        const cachedCmds = commandByDirCache.get(dirKey);
        let cmds =
          cachedCmds && Date.now() <= cachedCmds.expiresAt ? cachedCmds.commands : null;
        const cachedSkills = skillsByDirCache.get(dirKey);
        let skills =
          cachedSkills && Date.now() <= cachedSkills.expiresAt ? cachedSkills.skills : null;
        if (
          !skills &&
          skillsStore.skills.length > 0 &&
          (!effectiveDir || !skillsStore.directory || skillsStore.directory === effectiveDir)
        ) {
          skills = skillsStore.skills;
        }

        const publish = () => {
          if (reqID !== requestGen) return;
          const filtered = buildSlashCommandSuggestions({
            commands: cmds ?? [],
            skills: skills ?? [],
            query,
            runtime,
          });
          setSuggestions(filtered);
          setSelectedIndex(0);
        };

        if (cmds || skills) {
          publish();
        }

        const tasks: Promise<void>[] = [];
        if (!cmds) {
          tasks.push(
            hoxiAPI
              .listCommands(effectiveDir)
              .then((res) => {
                cmds = res ?? [];
                if (cmds.length > 0) {
                  commandByDirCache.set(dirKey, {
                    commands: cmds,
                    expiresAt: Date.now() + COMMAND_CACHE_TTL_MS,
                  });
                }
                publish();
              })
              .catch(() => {
                cmds = cmds ?? [];
              })
          );
        }
        if (!cachedSkills || Date.now() > cachedSkills.expiresAt) {
          tasks.push(
            skillsAPI
              .fetchSkillsList({
                directory: effectiveDir,
                sessionID: sid,
                runtime,
              })
              .then((res) => {
                skills = res.supported ? (res.skills ?? []) : [];
                recordSkillDescriptors(skills);
                if (skills.length > 0) {
                  skillsByDirCache.set(dirKey, {
                    skills,
                    expiresAt: Date.now() + COMMAND_CACHE_TTL_MS,
                  });
                }
                publish();
              })
              .catch(() => {
                skills = skills ?? [];
              })
          );
        }
        if (tasks.length > 0) {
          await Promise.all(tasks);
          publish();
        }
      })();
      return;
    }

    // 2. @file, @changes, @session, or @worktree mention at cursor
    const query = token.query;
    const trimmedQuery = query.trim();
    const qLower = trimmedQuery.toLowerCase();
    const reqID = ++requestGen;
    setTriggerMode("file");
    let cancelled = false;
    const cachedFiles = readCachedFileSearch(dir, trimmedQuery);
    const delayMs = cachedFiles ? 0 : 90;
    const timer = setTimeout(async () => {
      try {
        let files: string[] = cachedFiles ?? [];
        if (!cachedFiles) {
          if (trimmedQuery.length > 0) {
            files = (await hoxiAPI.findFiles(trimmedQuery, dir)) ?? [];
          } else {
            const nodes = await hoxiAPI.listFiles("", dir);
            files = (nodes ?? []).map((n) => n.path);
          }
          writeCachedFileSearch(dir, trimmedQuery, files);
        }
        if (cancelled || reqID !== requestGen) return;
        const contextItems: AutocompleteItem[] = [];
        if ("changes".includes(qLower)) {
          contextItems.push({
            type: "context",
            label: "@changes",
            detail: t("timeline.mentionChangesDetail"),
            insertText: "@changes ",
          });
        }
        if ("terminal".includes(qLower)) {
          contextItems.push({
            type: "context",
            label: "@terminal",
            detail: t("timeline.mentionTerminalDetail"),
            insertText: "@terminal ",
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

        const sessionQuery = qLower.startsWith("session:")
          ? qLower.slice("session:".length).trim()
          : qLower;
        let matchedSessions = 0;
        for (const s of workspace.sessions) {
          if (s.id === sessionID()) continue;
          const title = s.title || s.id.slice(0, 8);
          if (
            !sessionQuery ||
            "session".includes(sessionQuery) ||
            title.toLowerCase().includes(sessionQuery) ||
            s.id.toLowerCase().includes(sessionQuery)
          ) {
            contextItems.push({
              type: "context",
              label: `@session:${s.id}`,
              detail: t("timeline.mentionSessionDetail", { title }),
              insertText: `@session:${s.id} `,
            });
            matchedSessions++;
            if (matchedSessions >= 5) break;
          }
        }

        setSuggestions([
          ...contextItems.slice(0, 5),
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
    }, delayMs);
    onCleanup(() => {
      cancelled = true;
      clearTimeout(timer);
    });
  });

  const applySuggestion = (item: AutocompleteItem) => {
    const val = text();
    const token =
      activeToken() ??
      findAutocompleteToken(val, cursorOffset ? cursorOffset() : val.length);
    if (item.source === "skill" && item.skillSelection) {
      const sid = sessionID();
      selectComposerSkill(sid, item.skillSelection);
      setComposerSkillNotice(sid, null);
      if (token) {
        const before = val.slice(0, token.start);
        const rawAfter = val.slice(token.end);
        const after =
          before.length === 0 || /\s$/.test(before)
            ? rawAfter.replace(/^\s+/, "")
            : rawAfter;
        const nextText = `${before}${after}`;
        setText(nextText);
        setTriggerMode(null);
        setActiveToken(null);
        setSuggestions([]);
        onApplied?.(before.length);
        return;
      }
      const stripped = val.replace(/\/[^\s/@]*\s*/, "");
      setText(stripped);
      setTriggerMode(null);
      setActiveToken(null);
      setSuggestions([]);
      onApplied?.(0);
      return;
    }
    if (token) {
      const applied = applyAutocompleteToken(val, token, item.insertText);
      setText(applied.text);
      setTriggerMode(null);
      setActiveToken(null);
      setSuggestions([]);
      onApplied?.(applied.cursor);
      return;
    }
    if (item.type === "command") {
      setText(item.insertText);
      onApplied?.(item.insertText.length);
    } else {
      const next = val.replace(/@([^\s@]*)$/, () => item.insertText);
      setText(next);
      onApplied?.(next.length);
    }
    setTriggerMode(null);
    setActiveToken(null);
    setSuggestions([]);
  };

  const reset = () => {
    setTriggerMode(null);
    setActiveToken(null);
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
