// web/src/components/timeline/shell-command-summary.ts
import type { MessagePart, ToolPart } from "../../services/sse-types";
import { workspace } from "../../store/workspace";
import { isAttentionToolPart } from "./context-activity-parts";
import { isReadTool, isSearchOrListTool, isShellTool } from "./tool-taxonomy";

export interface ShellCommandIntent {
  kind: "read" | "search" | "list" | "ssh" | "command";
  summary: string;
  compactCommand: string;
  targetFile?: string;
  lineRange?: string;
  searchPattern?: string;
  searchPath?: string;
  sshHost?: string;
  sshRemoteCommand?: string;
  isReadOnlyExploration: boolean;
}

const LEADING_CD_RE =
  /^(?:(?:cd|pushd)\s+(?:'[^']*'|"[^"]*"|[^\s;&|]+)(?:\s+>\s*\/dev\/null(?:\s+2>&1)?)?\s*(?:&&|;)\s*)+/;

const TRAILING_PIPE_NOISE_RE =
  /(?:\s+2>&1|\s+2>\/dev\/null|\s*\|\s*(?:head|tail)(?:\s+-(?:n\s*\d+|c\s*\d+|\d+))?|\s*\|\s*cat)\s*$/;

const SSH_NO_ARG_FLAGS_RE = /^-[1246AaCfGgKkMNnqsTtVvXxYy]+$/;
const SSH_ARG_FLAG_EXACT_RE = /^-[bcDEeFIiJLlmOopQRSWw]$/;
const SSH_ARG_FLAG_INLINE_RE = /^-[bcDEeFIiJLlmOopQRSWw].+/;

const SEARCH_VALUE_FLAGS = new Set([
  "-e", "--regexp",
  "-g", "--glob", "--iglob",
  "-t", "--type", "--type-add", "--type-not",
  "-A", "--after-context",
  "-B", "--before-context",
  "-C", "--context",
  "-m", "--max-count",
  "--max-depth", "--max-filesize",
  "--encoding", "--color", "--colors",
  "--path-separator", "--sort", "--sortr",
  "--threads", "-j",
  "--include", "--exclude", "--exclude-dir",
]);

function unquoteShellWord(word: string): string {
  const trimmed = word.trim();
  if (trimmed.length >= 2) {
    if (trimmed.startsWith("'") && trimmed.endsWith("'")) {
      return trimmed.slice(1, -1).replace(/'\\''/g, "'");
    }
    if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
      return trimmed.slice(1, -1).replace(/\\(["\\$`])/g, "$1");
    }
  }
  return trimmed;
}

function tokenizeShellWords(line: string): string[] {
  const tokens: string[] = [];
  let current = "";
  let quote: "'" | '"' | null = null;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quote) {
      if (ch === quote) {
        quote = null;
      } else if (ch === "\\" && quote === '"' && i + 1 < line.length) {
        current += line[++i];
      } else {
        current += ch;
      }
      continue;
    }
    if (ch === "'" || ch === '"') {
      quote = ch;
      continue;
    }
    if (ch === "\\" && i + 1 < line.length) {
      current += line[++i];
      continue;
    }
    if (/\s/.test(ch)) {
      if (current.length > 0) {
        tokens.push(current);
        current = "";
      }
      continue;
    }
    current += ch;
  }
  if (current.length > 0) tokens.push(current);
  return tokens;
}

function hasUnquotedShellControl(line: string, allowSinglePipe = false): boolean {
  let quote: "'" | '"' | null = null;
  let pipeCount = 0;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quote) {
      if (ch === quote) quote = null;
      else if (ch === "\\" && quote === '"') i++;
      continue;
    }
    if (ch === "'" || ch === '"') {
      quote = ch;
      continue;
    }
    if (ch === "\\") {
      i++;
      continue;
    }
    if (ch === ";" || ch === "`" || ch === ">" || ch === "<") return true;
    if (ch === "$" && line[i + 1] === "(") return true;
    if (ch === "&") return true;
    if (ch === "|") {
      if (line[i + 1] === "|") return true;
      pipeCount++;
      if (!allowSinglePipe || pipeCount > 1) return true;
    }
  }
  return false;
}

function normalizeDisplayPath(rawPath: string): string {
  const cleaned = unquoteShellWord(rawPath).trim();
  if (!cleaned) return "";
  const root = workspace.rootDir?.replace(/\/+$/, "") || "";
  if (root && (cleaned === root || cleaned.startsWith(`${root}/`))) {
    return cleaned.slice(root.length).replace(/^\/+/, "") || ".";
  }
  return cleaned;
}

function shortBasename(p: string): string {
  const clean = p.replace(/[/\\]+$/, "");
  const parts = clean.split(/[/\\]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p;
}

export function stripShellBoilerplate(rawLine: string): string {
  let cur = rawLine.trim();
  const withoutCd = cur.replace(LEADING_CD_RE, "").trim();
  if (withoutCd) cur = withoutCd;
  for (let i = 0; i < 4; i++) {
    const next = cur.replace(TRAILING_PIPE_NOISE_RE, "").trim();
    if (!next || next === cur) break;
    cur = next;
  }
  return cur;
}

function parseSshIntent(compact: string): ShellCommandIntent | null {
  if (!/^ssh\s+/.test(compact)) return null;
  const tokens = tokenizeShellWords(compact);
  if (tokens[0] !== "ssh") return null;

  let i = 1;
  while (i < tokens.length) {
    const tok = tokens[i];
    if (tok === "--") {
      i++;
      break;
    }
    if (SSH_ARG_FLAG_EXACT_RE.test(tok)) {
      i += 2;
      continue;
    }
    if (SSH_ARG_FLAG_INLINE_RE.test(tok) || SSH_NO_ARG_FLAGS_RE.test(tok) || tok.startsWith("-")) {
      i++;
      continue;
    }
    break;
  }

  const host = tokens[i];
  if (!host) return null;
  const remoteRaw = tokens.slice(i + 1).join(" ").trim();
  const remoteClean = remoteRaw ? stripShellBoilerplate(unquoteShellWord(remoteRaw)) : "";
  const summary = remoteClean ? `${host} · ${remoteClean}` : host;
  return {
    kind: "ssh",
    sshHost: host,
    sshRemoteCommand: remoteClean || undefined,
    summary,
    compactCommand: remoteClean ? `ssh ${host} '${remoteClean}'` : `ssh ${host}`,
    isReadOnlyExploration: false,
  };
}

const SED_READ_RE =
  /^(?:nl\s+(?:-[a-zA-Z0-9]+\s+)*([^\s|;&>]+)\s*\|\s*)?sed\s+-n\s+(['"]?\d+(?:,\s*(?:\d+|\$))?p(?:\s*;\s*\d+q)?['"]?)(?:\s+([^\s|;&>]+))?$/;

function parseReadIntent(compact: string): ShellCommandIntent | null {
  const sedMatch = SED_READ_RE.exec(compact);
  if (sedMatch && !hasUnquotedShellControl(compact, true)) {
    const rawFile = sedMatch[3] || sedMatch[1];
    const expr = unquoteShellWord(sedMatch[2]);
    const rangeMatch = /^(\d+)(?:,\s*(\d+|\$))?p/.exec(expr);
    if (rawFile && !rawFile.startsWith("-") && rangeMatch) {
      const targetFile = normalizeDisplayPath(rawFile);
      const start = rangeMatch[1];
      const end = rangeMatch[2] === "$" ? "end" : rangeMatch[2];
      const lineRange = end && end !== start ? `${start}-${end}` : start;
      const base = shortBasename(targetFile);
      return {
        kind: "read",
        targetFile,
        lineRange,
        summary: `${base}:${lineRange}`,
        compactCommand: compact,
        isReadOnlyExploration: true,
      };
    }
  }

  if (hasUnquotedShellControl(compact, false)) return null;
  const tokens = tokenizeShellWords(compact);
  if (tokens.length < 2) return null;
  const cmd = tokens[0];

  if (cmd === "cat" || cmd === "bat") {
    const files: string[] = [];
    for (let i = 1; i < tokens.length; i++) {
      const tok = tokens[i];
      if (tok.startsWith("-")) continue;
      files.push(tok);
    }
    if (files.length === 1) {
      const targetFile = normalizeDisplayPath(files[0]);
      if (!targetFile) return null;
      return {
        kind: "read",
        targetFile,
        summary: shortBasename(targetFile),
        compactCommand: compact,
        isReadOnlyExploration: true,
      };
    }
    return null;
  }

  if (cmd === "head" || cmd === "tail") {
    let count: string | undefined;
    const files: string[] = [];
    for (let i = 1; i < tokens.length; i++) {
      const tok = tokens[i];
      if (tok === "-f" || tok === "-F" || tok === "--follow") return null;
      if ((tok === "-n" || tok === "-c") && i + 1 < tokens.length) {
        count = tokens[++i].replace(/^\+/, "");
        continue;
      }
      const inlineNum = /^-(?:n)?(\d+)$/.exec(tok);
      if (inlineNum) {
        count = inlineNum[1];
        continue;
      }
      if (tok.startsWith("-")) continue;
      files.push(tok);
    }
    if (files.length === 1) {
      const targetFile = normalizeDisplayPath(files[0]);
      if (!targetFile) return null;
      const lineRange = count ? (cmd === "head" ? `1-${count}` : `tail ${count}`) : undefined;
      const base = shortBasename(targetFile);
      return {
        kind: "read",
        targetFile,
        lineRange,
        summary: lineRange ? `${base}:${lineRange}` : base,
        compactCommand: compact,
        isReadOnlyExploration: true,
      };
    }
  }

  return null;
}

function parseSearchOrListIntent(compact: string): ShellCommandIntent | null {
  if (hasUnquotedShellControl(compact, false)) return null;
  const tokens = tokenizeShellWords(compact);
  if (tokens.length === 0) return null;

  const isGitGrep = tokens[0] === "git" && tokens[1] === "grep";
  const isSearchBin =
    isGitGrep ||
    tokens[0] === "rg" ||
    tokens[0] === "ripgrep" ||
    tokens[0] === "grep" ||
    tokens[0] === "egrep" ||
    tokens[0] === "fgrep";

  if (isSearchBin) {
    const startIdx = isGitGrep ? 2 : 1;
    if (tokens.slice(startIdx).includes("--files")) {
      const pathArg = tokens.slice(startIdx).find((t) => !t.startsWith("-"));
      const cleanPath = pathArg ? normalizeDisplayPath(pathArg) : "";
      return {
        kind: "list",
        searchPath: cleanPath || undefined,
        summary: cleanPath ? shortBasename(cleanPath) : compact,
        compactCommand: compact,
        isReadOnlyExploration: true,
      };
    }

    let explicitPattern: string | undefined;
    let positionalPattern: string | undefined;
    const paths: string[] = [];
    let afterDoubleDash = false;

    for (let i = startIdx; i < tokens.length; i++) {
      const tok = tokens[i];
      if (!afterDoubleDash && tok === "--") {
        afterDoubleDash = true;
        continue;
      }
      if (!afterDoubleDash) {
        if (tok === "-f" || tok === "--file" || tok.startsWith("--file=")) {
          return null;
        }
        if ((tok === "-e" || tok === "--regexp") && i + 1 < tokens.length) {
          explicitPattern = explicitPattern ?? tokens[++i];
          continue;
        }
        if (tok.startsWith("-e") && tok.length > 2) {
          explicitPattern = explicitPattern ?? tok.slice(2);
          continue;
        }
        if (tok.startsWith("--regexp=")) {
          explicitPattern = explicitPattern ?? tok.slice("--regexp=".length);
          continue;
        }
        if (SEARCH_VALUE_FLAGS.has(tok)) {
          i++;
          continue;
        }
        if (tok.startsWith("-")) continue;
      }
      if (explicitPattern === undefined && positionalPattern === undefined) {
        positionalPattern = tok;
      } else {
        paths.push(tok);
      }
    }

    const pattern = (explicitPattern ?? positionalPattern)?.trim();
    if (pattern) {
      const cleanPaths = paths
        .map(normalizeDisplayPath)
        .filter((p) => p && p !== "." && p !== "./");
      const searchPath = cleanPaths[0];
      const summary = searchPath
        ? `"${pattern}" (${shortBasename(searchPath)})`
        : `"${pattern}"`;
      return {
        kind: "search",
        searchPattern: pattern,
        searchPath,
        summary,
        compactCommand: compact,
        isReadOnlyExploration: true,
      };
    }
  }

  if (tokens[0] === "ls" || tokens[0] === "tree" || tokens[0] === "pwd") {
    const target = tokens.slice(1).find((t) => !t.startsWith("-"));
    const cleanTarget = target ? normalizeDisplayPath(target) : "";
    return {
      kind: "list",
      searchPath: cleanTarget || undefined,
      summary: cleanTarget ? shortBasename(cleanTarget) : compact,
      compactCommand: compact,
      isReadOnlyExploration: true,
    };
  }

  if (tokens[0] === "find" || tokens[0] === "fd") {
    if (tokens.some((t) => t === "-exec" || t === "-execdir" || t === "-delete" || t === "-ok")) {
      return null;
    }
    return {
      kind: "list",
      summary: compact,
      compactCommand: compact,
      isReadOnlyExploration: true,
    };
  }

  if (
    tokens[0] === "git" &&
    (tokens[1] === "status" ||
      tokens[1] === "diff" ||
      tokens[1] === "log" ||
      tokens[1] === "show" ||
      tokens[1] === "rev-parse" ||
      tokens[1] === "blame")
  ) {
    return {
      kind: "command",
      summary: compact,
      compactCommand: compact,
      isReadOnlyExploration: true,
    };
  }

  return null;
}

export function analyzeShellCommand(rawCommand: string): ShellCommandIntent {
  const trimmed = (rawCommand || "").trim();
  if (!trimmed) {
    return {
      kind: "command",
      summary: "",
      compactCommand: "",
      isReadOnlyExploration: false,
    };
  }
  const lines = trimmed
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
  const firstLine = lines[0] ?? "";
  const isMultiLine = lines.length > 1;
  const compact = stripShellBoilerplate(firstLine);

  const sshIntent = parseSshIntent(compact);
  if (sshIntent) return sshIntent;

  if (!isMultiLine) {
    const readIntent = parseReadIntent(compact);
    if (readIntent) return readIntent;

    const searchOrListIntent = parseSearchOrListIntent(compact);
    if (searchOrListIntent) return searchOrListIntent;
  }

  return {
    kind: "command",
    summary: compact || firstLine,
    compactCommand: compact || firstLine,
    isReadOnlyExploration: false,
  };
}

export function isFoldableExplorationStep(
  part: MessagePart,
  extractCmd: (p: ToolPart) => string
): part is ToolPart {
  if (part.type !== "tool") return false;
  const toolPart = part as ToolPart;
  const status = toolPart.state.status;
  if (status !== "completed" && status !== "running" && status !== "pending") {
    return false;
  }
  if (Boolean(isAttentionToolPart(toolPart))) return false;
  if (isReadTool(toolPart.tool) || isSearchOrListTool(toolPart.tool)) return true;
  if (isShellTool(toolPart.tool)) {
    return analyzeShellCommand(extractCmd(toolPart)).isReadOnlyExploration;
  }
  return false;
}

function isPendingUnresolvedShellStep(
  part: MessagePart,
  extractCmd: (p: ToolPart) => string
): part is ToolPart {
  if (part.type !== "tool") return false;
  const toolPart = part as ToolPart;
  return (
    toolPart.state.status === "pending" &&
    isShellTool(toolPart.tool) &&
    !extractCmd(toolPart).trim()
  );
}

export type ActivityStepWindowItem =
  | { kind: "part"; part: MessagePart }
  | { kind: "exploration-fold"; key: string; parts: ToolPart[] };

const partItemCache = new WeakMap<MessagePart, { kind: "part"; part: MessagePart }>();

function wrapPartItem(part: MessagePart): { kind: "part"; part: MessagePart } {
  let item = partItemCache.get(part);
  if (!item) {
    item = { kind: "part", part };
    partItemCache.set(part, item);
  }
  return item;
}

export function stabilizeActivityStepItems(
  prev: ActivityStepWindowItem[] | undefined,
  next: ActivityStepWindowItem[]
): ActivityStepWindowItem[] {
  if (!prev || prev.length === 0) return next;
  let reusedAll = prev.length === next.length;
  const stabilized = next.map((item, idx) => {
    const prior = prev[idx];
    if (!prior || prior.kind !== item.kind) {
      reusedAll = false;
      return item;
    }
    if (item.kind === "part" && prior.kind === "part") {
      if (prior.part === item.part) return prior;
      reusedAll = false;
      return item;
    }
    if (item.kind === "exploration-fold" && prior.kind === "exploration-fold") {
      if (prior.key === item.key) return prior;
      reusedAll = false;
      return item;
    }
    reusedAll = false;
    return item;
  });
  return reusedAll ? prev : stabilized;
}

export function groupVisibleActivitySteps(
  parts: MessagePart[],
  extractCmd: (p: ToolPart) => string,
  disableFold = false
): ActivityStepWindowItem[] {
  if (disableFold || parts.length < 2) {
    return parts.map(wrapPartItem);
  }
  const out: ActivityStepWindowItem[] = [];
  let run: ToolPart[] = [];

  const flushRun = () => {
    if (run.length === 0) return;
    if (run.length === 1) {
      out.push(wrapPartItem(run[0]));
    } else {
      out.push({
        kind: "exploration-fold",
        key: `fold:${run[0].id}`,
        parts: run,
      });
    }
    run = [];
  };

  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    if (
      isFoldableExplorationStep(part, extractCmd) ||
      (i === parts.length - 1 &&
        run.length > 0 &&
        isPendingUnresolvedShellStep(part, extractCmd))
    ) {
      run.push(part);
    } else {
      flushRun();
      out.push(wrapPartItem(part));
    }
  }
  flushRun();
  return out;
}
