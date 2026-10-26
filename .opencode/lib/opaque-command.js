// Opaque shell commands must ask in every approval mode. Mirrors
// isOpaqueShellCommand in internal/runner/permission_policy.go and the loop
// narrowing in internal/runner/opaque_loops.go: a variable is only trusted when
// it is a loop variable bounded by the same command (`for x in` literal,
// non-secret items, or `while read x` fed by a file listing that cannot name a
// hidden or secret file) and it only feeds read-only tools. The shared
// .opencode/lib/opaque-command.cases.json keeps both ports aligned.
import { DATA_ARG_INTERPRETERS } from "./command-data-args.js";
import {
  CD_SEGMENT_RE,
  DOLLAR_EXPAND_RE,
  commandCandidates,
  isProtectedPath,
  isSecretPath,
  isSensitiveExternalPath,
  isWordBoundary,
  isWorktreeCD,
  normalizeAbsoluteTool,
  shellWords,
  splitCommandChainWithPipes,
  stripNullRedirects,
  unwrapSandboxCommand,
} from "./permission-policy.js";
import { awkProgramExecutes, inlineScriptTool } from "./script-interpreter.js";
import {
  CMD_SUBST_VAR,
  extractProcessSubstitutions,
  joinLineContinuations,
  stripLiteralHeredocs,
} from "./shell-substitution.js";
import {
  HARMLESS_ENV_VARS,
  MAX_SUBSHELL_DEPTH,
  READ_ONLY_DATA_TOOLS,
  VALUE_ONLY_TOOLS,
  acceptsValueClassArg,
  commandSubsAreValueClass,
  concretizeLiterals,
  eraseSubshells,
  findSubshellSpans,
  isHarmlessSet,
  setLiteralJudge,
} from "./shell-constants.js";

const BENIGN_PATH_EXPORT_RE = /^export[ \t]+PATH=[A-Za-z0-9_./:$~{}-]+$/;
const ENV_VAR_ASSIGNMENT_WORD_RE = /^[A-Za-z_][A-Za-z0-9_]*=/;
const RM_FLAG_RE = /^(?:-[rRfdvi]+|--(?:recursive|force|dir|verbose))$/;
const INLINE_SCRIPT_FLAGS = new Set(["-c", "-e", "--eval", "-p", "--print", "-"]);
const INTERP_VALUE_FLAGS = new Set(["-m", "-W", "-X", "-r", "--require", "--import", "-I", "-C"]);

const SHELL_KEYWORDS = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);
const LOOP_VAR_REBINDERS = new Set(["declare", "local", "typeset", "readonly", "mapfile", "readarray", "let", "unset", "export"]);
const SHELL_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const EXT_GLOB_RE = /^\*\.[A-Za-z0-9]+$/;
const GIT_EXT_PATHSPEC_RE = /^[A-Za-z0-9_./-]*\*\.[A-Za-z0-9]+$/;
const RG_LIST_FLAG_RE = /^-[a-zA-Z]*l[a-zA-Z]*$/;
const RG_UNSAFE_SHORT_RE = /^-[a-zA-Z.]*[u.L][a-zA-Z.]*$/;
const DOT_GLOB_RE = /(^|\/)\.[^/]*[*?]/;
// Hold system locations and settings, never secrets.
const GIT_GLOBAL_VALUE_FLAGS = new Set(["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"]);

const baseName = (w) => (w.includes("/") ? w.slice(w.lastIndexOf("/") + 1) : w);

function hasOpaqueShellSyntax(s) {
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const prev = i > 0 ? s[i - 1] : "";
    const next = i + 1 < s.length ? s[i + 1] : "";
    if (quote === "'") {
      if (c === "'") {
        quote = "";
        if (next && !isWordBoundary(next)) return true;
      }
      continue;
    }
    if (c === "`" || (c === "$" && next === "(")) return true;
    if (quote === '"') {
      if (c === "\\" && i + 1 < s.length) {
        i++;
        continue;
      }
      if (c === '"') {
        quote = "";
        if (next && !isWordBoundary(next)) return true;
      }
      continue;
    }
    if (c === "\\" && (next === "[" || next === "]")) {
      i++;
      continue;
    }
    if (c === "\\" || c === "(" || c === ")" || c === "{" || c === "}") return true;
    if (c === "<" && next === "<") return true;
    if (c === "'" || c === '"') {
      const shortFlag = i >= 2 && s[i - 2] === "-" && (i === 2 || isWordBoundary(s[i - 3])) && /[A-Za-z]/.test(prev);
      if (prev && !isWordBoundary(prev) && prev !== "=" && !shortFlag) return true;
      quote = c;
    }
  }
  return quote !== "";
}

function hasDollarExpansion(s) {
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const next = i + 1 < s.length ? s[i + 1] : "";
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (quote === '"') {
      if (c === "\\" && i + 1 < s.length) {
        i++;
      } else if (c === '"') {
        quote = "";
      } else if (c === "$" && next && next !== '"' && DOLLAR_EXPAND_RE.test(next)) {
        return true;
      }
    } else if (c === "'" || c === '"') {
      quote = c;
    } else if (c === "$" && next && DOLLAR_EXPAND_RE.test(next)) {
      return true;
    }
  }
  return false;
}

function isBenignPathExport(seg) {
  const trimmed = String(seg || "").trim();
  if (!BENIGN_PATH_EXPORT_RE.test(trimmed)) return false;
  const rest = trimmed.replaceAll("${PATH}", "").replaceAll("$PATH", "");
  return !rest.includes("$");
}

function cleanRelPosixPath(p) {
  const parts = [];
  for (const seg of p.split("/")) {
    if (!seg || seg === ".") continue;
    if (seg === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") parts.pop();
      else parts.push("..");
    } else {
      parts.push(seg);
    }
  }
  return parts.join("/") || ".";
}

function isSafeExternalCDTarget(resolved) {
  return Boolean(resolved) && resolved !== "/" && !isSensitiveExternalPath(resolved) && !isSecretPath(resolved) && !isProtectedPath(resolved);
}

function advanceScopedCD(seg, cwd, worktree) {
  if (!cwd) return ["", false];
  let trimmed = String(seg || "").trim();
  if (trimmed.startsWith("cd ") || trimmed.startsWith("cd\t")) {
    const arg = trimmed.slice(2).trim();
    if (arg.length >= 2 && ((arg.startsWith('"') && arg.endsWith('"')) || (arg.startsWith("'") && arg.endsWith("'")))) {
      trimmed = "cd " + arg.slice(1, -1);
    }
  }
  const m = CD_SEGMENT_RE.exec(trimmed);
  if (!m) return ["", false];
  const target = m[1];
  if (target.startsWith("~") || target.startsWith("-")) return ["", false];
  if (target.startsWith("/")) {
    if (target.split("/").includes("..")) return ["", false];
    const rel = cleanRelPosixPath(target);
    const cleaned = rel === "." ? "/" : "/" + rel;
    if (!isSafeExternalCDTarget(cleaned)) return ["", false];
    return [cleaned, true];
  }
  const wt = String(worktree || "").trim();
  if (cwd.startsWith("/")) {
    const rel = cleanRelPosixPath(`${cwd}/${target}`);
    const resolved = rel === "." || rel === ".." || rel.startsWith("../") ? "/" : "/" + rel;
    if (wt && wt.startsWith("/")) {
      const base = "/" + cleanRelPosixPath(wt);
      if (cwd === base || cwd.startsWith(base + "/")) {
        if (!isWorktreeCD(`cd ${resolved}`, wt)) return ["", false];
        return [resolved, true];
      }
    }
    if (!isSafeExternalCDTarget(resolved)) return ["", false];
    return [resolved, true];
  }
  const cleaned = cleanRelPosixPath(`${cwd}/${target}`);
  if (cleaned === ".." || cleaned.startsWith("../") || isSensitiveExternalPath(cleaned) || isSecretPath(cleaned) || isProtectedPath(cleaned)) {
    return ["", false];
  }
  return [cleaned, true];
}

function isTempOnlyCleanupSegment(words) {
  const cleaned = stripNullRedirects(words);
  if (!cleaned || cleaned.length === 0) return false;
  const cmd = normalizeAbsoluteTool(cleaned[0]);
  const base = cmd.includes("/") ? cmd.slice(cmd.lastIndexOf("/") + 1) : cmd;
  if (base !== "rm" && base !== "rmdir" && base !== "unlink") return false;
  let i = 1;
  while (i < cleaned.length && cleaned[i] !== "--" && cleaned[i].startsWith("-")) {
    if (!RM_FLAG_RE.test(cleaned[i])) return false;
    i++;
  }
  if (i < cleaned.length && cleaned[i] === "--") i++;
  const paths = cleaned.slice(i);
  return paths.length > 0 && paths.every((p) => (p.startsWith("/tmp/") || p.startsWith("/private/tmp/")) && !p.includes("..") && !/[$[~]/.test(p));
}

function hasInlineScriptFlag(base, args) {
  if (args.length === 0) return true;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--") return false;
    if (a === "-" || INLINE_SCRIPT_FLAGS.has(a) || a.startsWith("-c") || a.startsWith("-e") || a.startsWith("--eval=") || a.startsWith("--print=")) {
      return true;
    }
    if ((base === "perl" || base === "ruby") && a.startsWith("-") && !a.startsWith("--") && /[ep]/.test(a.slice(1))) return true;
    if (base === "php" && a.startsWith("-r")) return true;
    if (INTERP_VALUE_FLAGS.has(a)) { i++; continue; }
    if (!a.startsWith("-")) return false;
  }
  return false;
}

export function isOpaqueShellCommand(command, worktree = "", options = {}) {
  return isOpaqueShellCommandDepth(command, worktree, options, 0);
}

function isOpaqueShellCommandDepth(command, worktree, options, depth) {
  if (depth > MAX_SUBSHELL_DEPTH) return true;
  const allowInlineScripts = Boolean(options.allowInlineScripts);
  const trimmed = unwrapSandboxCommand(command).trim();
  if (!trimmed) return true;
  let joined = joinLineContinuations(trimmed);
  if (joined.includes("<<")) {
    joined = stripLiteralHeredocs(joined);
    if (joined === null) return true;
  }
  const spans = findSubshellSpans(joined);
  if (!spans) return true;
  if (spans.every(([start, end]) => !isOpaqueShellCommandDepth(joined.slice(start + 1, end), worktree, options, depth + 1))) {
    const erased = eraseSubshells(joined);
    if (!opaqueShellCore(erased, worktree, allowInlineScripts, commandSubsAreValueClass(erased))) return false;
  }
  const expanded = concretizeLiterals(joined);
  if (expanded.status === "secret" || expanded.status === "reject") return true;
  if (expanded.status === "applied") {
    const expSpans = findSubshellSpans(expanded.text);
    if (!expSpans || expSpans.some(([start, end]) => isOpaqueShellCommandDepth(expanded.text.slice(start + 1, end), worktree, options, depth + 1))) {
      return true;
    }
    const erased = eraseSubshells(expanded.text);
    return opaqueShellCore(erased, worktree, allowInlineScripts, commandSubsAreValueClass(erased));
  }
  return true;
}

function opaqueShellCore(command, worktree, allowInlineScripts, valueClass) {
  command = String(command ?? "").trim();
  if (!command) return true;
  const unwrapped = extractProcessSubstitutions(command);
  if (hasOpaqueShellSyntax(unwrapped)) return true;
  let { segments, pipedIn } = splitCommandChainWithPipes(unwrapped);
  let cwd = String(worktree || "").trim() || ".";
  if (segments.length > 1 && !pipedIn[0] && isWorktreeCD(segments[0], worktree)) {
    const [nextCWD, ok] = advanceScopedCD(segments[0], cwd, worktree);
    if (ok) cwd = nextCWD;
    segments = segments.slice(1);
    pipedIn = pipedIn.slice(1);
  }
  if (segments.length === 0) return true;
  let outsideCWD = false;
  let hasExec = false;
  const vars = loopBindings(segments, pipedIn);
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i].trim();
    if (!seg || seg.startsWith("#")) continue;
    const words = stripShellKeywords(shellWords(seg));
    if (words.length === 0) continue;
    if (hasDollarExpansion(seg) && !isBenignPathExport(seg) && !expansionsAreBounded(seg, words, vars, valueClass)) return true;
    if (words[0] === "for") continue;
    hasExec = true;
    let cmdIdx = 0;
    while (cmdIdx < words.length && ENV_VAR_ASSIGNMENT_WORD_RE.test(words[cmdIdx])) cmdIdx++;
    if (cmdIdx >= words.length) continue;
    const unwrapped = unwrapCommandPrefixes(words.slice(cmdIdx));
    if (!unwrapped) return true;
    if (unwrapped.lookupOnly) continue;
    const cmdWords = unwrapped.words;
    const norm = normalizeAbsoluteTool(cmdWords[0]);
    const base = norm.includes("/") ? norm.slice(norm.lastIndexOf("/") + 1) : norm;
    const args = cmdWords.slice(1);
    if (base === "set") {
      if (!isHarmlessSet(cmdWords)) return true;
      continue;
    }
    if (base === "cd" || base === "pushd" || base === "popd") {
      if (base !== "cd" || outsideCWD) {
        outsideCWD = true;
        cwd = "";
      } else {
        const [nextCWD, ok] = advanceScopedCD(seg, cwd, worktree);
        if (ok) cwd = nextCWD;
        else {
          outsideCWD = true;
          cwd = "";
        }
      }
      continue;
    }
    if (outsideCWD && !isTempOnlyCleanupSegment(cmdWords)) return true;
    if (DATA_ARG_INTERPRETERS.has(base)) return true;
    if (awkProgramExecutes(base, args)) return true;
    const family = inlineScriptTool(base);
    if (family && !allowInlineScripts && (pipedIn[i] || hasInlineScriptFlag(family, args))) {
      return true;
    }
  }
  return !hasExec;
}

export function isOpaquePermissionRequest(input, options) {
  const worktree = options?.worktree || input?.metadata?.worktree || "";
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  const meta = input?.metadata || {};
  // OpenCode also sends its own split of the command in patterns; judged alone,
  // a piece loses the loop that binds its variable, so judge the whole command.
  const whole = typeof meta.command === "string" ? meta.command.trim() : "";
  const candidates = whole ? [unwrapSandboxCommand(whole)] : commandCandidates(input);
  const isBash =
    permission === "bash" ||
    permission === "shell" ||
    permission === "run_command" ||
    typeof meta.command === "string" ||
    typeof meta.cmd === "string" ||
    Array.isArray(meta.commands);
  if (!isBash) return false;
  if (candidates.length === 0) return true;
  const allowInlineScripts = Boolean(options?.allowInlineScripts);
  return candidates.some((cmd) => isOpaqueShellCommand(cmd, worktree, { allowInlineScripts }));
}

function stripShellKeywords(words) {
  let i = 0;
  while (i < words.length && SHELL_KEYWORDS.has(words[i])) i++;
  return words.slice(i);
}

function onlyIFSAssignments(words) {
  return words.every((w) => w.startsWith("IFS="));
}

function lastVarName(args) {
  for (let i = args.length - 1; i >= 0; i--) if (SHELL_VAR_NAME_RE.test(args[i])) return args[i];
  return "";
}

function readBinding(cmd) {
  const names = [];
  for (const a of cmd.slice(1)) {
    if (a === "-r") continue;
    if (a.startsWith("-") || !SHELL_VAR_NAME_RE.test(a)) return [lastVarName(cmd.slice(1)), false];
    names.push(a);
  }
  if (names.length !== 1) return [lastVarName(cmd.slice(1)), false];
  return [names[0], true];
}

function forLoopBinding(seg, cmd) {
  if (cmd.length < 2 || !SHELL_VAR_NAME_RE.test(cmd[1])) return ["", false];
  const name = cmd[1];
  if (cmd.length < 4 || cmd[2] !== "in" || hasDollarExpansion(seg) || seg.includes("`")) return [name, false];
  for (const item of cmd.slice(3)) {
    if (item.includes("[") || DOT_GLOB_RE.test(item) || isSecretPath(item) || isSensitiveExternalPath(item) || isProtectedPath(item)) {
      return [name, false];
    }
  }
  return [name, true];
}

function isSafeListingProducer(seg) {
  if (hasDollarExpansion(seg) || seg.includes("`")) return false;
  const words = stripNullRedirects(shellWords(seg));
  if (!words || words.length === 0 || ENV_VAR_ASSIGNMENT_WORD_RE.test(words[0])) return false;
  const args = words.slice(1);
  const base = baseName(normalizeAbsoluteTool(words[0]));
  if (base === "rg") {
    let listing = false;
    let files = false;
    let filtered = false;
    for (let i = 0; i < args.length; i++) {
      const a = args[i];
      if (a === "--hidden" || a === "-L" || a === "--follow" || a.startsWith("--no-ignore") || a.startsWith("--pre") ||
          a.startsWith("--hostname-bin") || (a.startsWith("-") && !a.startsWith("--") && RG_UNSAFE_SHORT_RE.test(a))) {
        return false;
      }
      if (a === "--files-with-matches" || a === "--files-without-match" || RG_LIST_FLAG_RE.test(a)) listing = true;
      else if (a === "--files") files = true;
      else if ((a === "-g" || a === "--glob") && i + 1 < args.length) {
        filtered = filtered || EXT_GLOB_RE.test(args[i + 1]);
        i++;
      } else if (a.startsWith("--glob=")) filtered = filtered || EXT_GLOB_RE.test(a.slice("--glob=".length));
      else if ((a === "-t" || a === "--type") && i + 1 < args.length) {
        filtered = true;
        i++;
      } else if (a.startsWith("--type=")) filtered = true;
    }
    return listing || (files && filtered);
  }
  if (base === "find") {
    let named = false;
    for (let i = 0; i < args.length; i++) {
      const a = args[i];
      if (!a.startsWith("-") && a !== "!" && a !== "(" && a !== ")") {
        if (named) return false;
      } else if (a === "-name" || a === "-iname") {
        if (i + 1 >= args.length || !EXT_GLOB_RE.test(args[i + 1])) return false;
        named = true;
        i++;
      } else if (a === "-type" || a === "-maxdepth" || a === "-mindepth") {
        i++;
      } else if (!["-not", "!", "-o", "-or", "-a", "-and", "-print"].includes(a)) {
        return false;
      }
    }
    return named;
  }
  if (base === "git") {
    if (args.length < 2 || args[0] !== "ls-files") return false;
    return args.slice(1).every((a) => a === "--" || GIT_EXT_PATHSPEC_RE.test(a));
  }
  return false;
}

function loopBindings(segments, pipedIn) {
  const vars = new Map();
  const bind = (name, ok) => vars.set(name, vars.has(name) ? vars.get(name) && ok : ok);
  segments.forEach((seg, i) => {
    const words = stripShellKeywords(shellWords(seg));
    let j = 0;
    while (j < words.length && ENV_VAR_ASSIGNMENT_WORD_RE.test(words[j])) {
      if (!isBenignPathAssignment(words[j])) bind(words[j].slice(0, words[j].indexOf("=")), false);
      j++;
    }
    if (j >= words.length) return;
    const cmd = words.slice(j);
    const base = baseName(cmd[0]);
    if (base === "for") {
      const [name, ok] = forLoopBinding(seg, cmd);
      if (name) bind(name, ok);
    } else if (base === "read") {
      const [name, ok] = readBinding(cmd);
      if (name) {
        bind(name, ok && onlyIFSAssignments(words.slice(0, j)) && i > 0 && pipedIn[i] && isSafeListingProducer(segments[i - 1]));
      }
    } else if (base === "printf") {
      for (let k = 1; k + 1 < cmd.length; k++) if (cmd[k] === "-v") bind(cmd[k + 1], false);
    } else if (LOOP_VAR_REBINDERS.has(base)) {
      for (const a of cmd.slice(1)) {
        if (isBenignPathAssignment(a)) continue;
        const name = a.includes("=") && a.indexOf("=") > 0 ? a.slice(0, a.indexOf("=")) : a;
        if (SHELL_VAR_NAME_RE.test(name)) bind(name, false);
      }
    }
  });
  return vars;
}

function shellExpansionNames(s) {
  const names = [];
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\") {
      i++;
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : '"';
      continue;
    }
    if (c === "'" && quote === "") {
      quote = "'";
      continue;
    }
    if (c !== "$" || i + 1 >= s.length) continue;
    const next = s[i + 1];
    if (next === "{") {
      const end = s.indexOf("}", i + 2);
      const name = end < 0 ? "" : s.slice(i + 2, end);
      if (!SHELL_VAR_NAME_RE.test(name)) return null;
      names.push(name);
      i = end;
    } else if (/[A-Za-z_]/.test(next)) {
      let k = i + 1;
      while (k < s.length && /[A-Za-z0-9_]/.test(s[k])) k++;
      names.push(s.slice(i + 1, k));
      i = k - 1;
    } else if (next === "?" || next === "!") {
      names.push(next);
      i++;
    } else if (DOLLAR_EXPAND_RE.test(next) && !(quote === '"' && next === '"')) {
      return null;
    }
  }
  return names;
}

// Strips wrappers that only run the next command (nohup, time, exec, command,
// env NAME=value); `command -v x` is a lookup. A bare `env` prints every
// environment variable, secrets included, so it returns null (opaque).
function unwrapCommandPrefixes(words) {
  while (words.length > 0) {
    const base = baseName(normalizeAbsoluteTool(words[0]));
    if (base === "nohup" || base === "time" || base === "exec") {
      if (words.length === 1) return { words, lookupOnly: false };
      words = words.slice(1);
    } else if (base === "command") {
      if (words.length >= 2 && (words[1] === "-v" || words[1] === "-V")) return { words, lookupOnly: true };
      if (words.length === 1) return { words, lookupOnly: false };
      words = words.slice(1);
    } else if (base === "env") {
      let k = 1;
      while (k < words.length && ENV_VAR_ASSIGNMENT_WORD_RE.test(words[k])) k++;
      if (k === 1 || k >= words.length) return null;
      words = words.slice(k);
    } else {
      return { words, lookupOnly: false };
    }
  }
  return { words, lookupOnly: false };
}

// Expansions only in the message of `git commit|tag|merge|stash -m …`.
function gitMessageExpansionsOnly(args, vars) {
  let i = 0;
  while (i < args.length && args[i].startsWith("-")) {
    if (GIT_GLOBAL_VALUE_FLAGS.has(args[i])) i++;
    i++;
  }
  if (i >= args.length) return false;
  const sub = args[i];
  if (!["commit", "tag", "merge", "stash"].includes(sub)) return false;
  for (let k = i + 1; k < args.length; k++) {
    const a = args[k];
    if (!a.includes("$")) continue;
    const prev = args[k - 1];
    const isMessage = prev === "-m" || prev === "--message" ||
      (sub === "commit" && prev.length > 2 && prev[0] === "-" && prev[1] !== "-" && prev.endsWith("m"));
    if (!isMessage) return false;
    const names = wordExpansionNames(a);
    if (names === null) return false;
    for (const name of names) {
      const bound = vars.has(name);
      if ((bound && vars.get(name) !== true) || (!bound && name !== CMD_SUBST_VAR && !HARMLESS_ENV_VARS.has(name))) {
        return false;
      }
    }
  }
  return true;
}

const GIT_LOOP_READ_SUBS = new Set(["status", "diff", "log", "show", "rev-parse", "ls-files", "ls-remote", "describe", "shortlog"]);
const GIT_LOOP_OUTPUT_RE = /^--(o(u[^=]*)?|ex(t[^=]*)?)(=|$)/;
const GIT_LOOP_BRANCH_SHORT_RE = /^-(?:[arv]+)$/;
const GIT_LOOP_BRANCH_LONG_RE = /^(?:--all|--remotes|--verbose|--show-current|--no-color)$/;

function isSafeLoopGitBranch(args) {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a.includes("$")) return false;
    if (a === "-l" || a === "--list") return args.slice(i + 1).every((x) => !x.startsWith("-") && !x.includes("$"));
    if (!GIT_LOOP_BRANCH_SHORT_RE.test(a) && !GIT_LOOP_BRANCH_LONG_RE.test(a)) return false;
  }
  return true;
}

function gitReadOnlyLoopExpansions(args, vars) {
  const onlyTrustedLoopVars = (word) => {
    const names = wordExpansionNames(word);
    return names !== null && names.length > 0 && names.every((n) => vars.has(n) && vars.get(n) === true);
  };
  let i = 0;
  while (i < args.length && args[i].startsWith("-")) {
    const a = args[i];
    if (a === "-c" || a.startsWith("-c") || a.startsWith("--config-env")) return false;
    if (a === "-C" || a === "--git-dir" || a === "--work-tree") {
      if (i + 1 >= args.length || (args[i + 1].includes("$") && !onlyTrustedLoopVars(args[i + 1]))) return false;
      i += 2;
      continue;
    }
    if (a.startsWith("-C") || a.startsWith("--git-dir=") || a.startsWith("--work-tree=")) {
      if (a.includes("$") && !onlyTrustedLoopVars(a)) return false;
      i++;
      continue;
    }
    if (a.includes("$") || GIT_GLOBAL_VALUE_FLAGS.has(a)) return false;
    i++;
  }
  if (i >= args.length) return false;
  const sub = args[i];
  const rest = args.slice(i + 1);
  if (sub === "branch") return isSafeLoopGitBranch(rest);
  if (!GIT_LOOP_READ_SUBS.has(sub)) return false;
  return rest.every((a) => !GIT_LOOP_OUTPUT_RE.test(a) && (!a.includes("$") || (!a.startsWith("-") && onlyTrustedLoopVars(a))));
}

function wordExpansionNames(word) {
  const names = [];
  for (let i = 0; i < word.length; i++) {
    if (word[i] !== "$" || i + 1 >= word.length) continue;
    const next = word[i + 1];
    if (next === "{") {
      const end = word.indexOf("}", i + 2);
      const name = end < 0 ? "" : word.slice(i + 2, end);
      if (!SHELL_VAR_NAME_RE.test(name)) return null;
      names.push(name);
      i = end;
    } else if (/[A-Za-z_]/.test(next)) {
      let k = i + 1;
      while (k < word.length && /[A-Za-z0-9_]/.test(word[k])) k++;
      names.push(word.slice(i + 1, k));
      i = k - 1;
    } else if (next === "?" || next === "!") {
      names.push(next);
      i++;
    } else {
      return null;
    }
  }
  return names;
}

function isBenignPathAssignment(word) {
  if (!word.startsWith("PATH=")) return false;
  const value = word.slice("PATH=".length);
  if (/[`;&|<>()]/.test(value)) return false;
  const names = wordExpansionNames(value);
  return names !== null && names.every((n) => HARMLESS_ENV_VARS.has(n));
}

function expansionsAreBounded(seg, words, vars, valueClass = false) {
  if (shellExpansionNames(seg) === null) return false;
  let j = 0;
  while (j < words.length && ENV_VAR_ASSIGNMENT_WORD_RE.test(words[j])) {
    if (words[j].includes("$") && !isBenignPathAssignment(words[j])) return false;
    j++;
  }
  if (j >= words.length) return true;
  if (words[j].includes("$")) return false;
  const base = baseName(normalizeAbsoluteTool(words[j]));
  const args = words.slice(j + 1);
  if (base === "export") return args.every((a) => !a.includes("$") || isBenignPathAssignment(a));
  const expanding = args.filter((a) => a.includes("$"));
  if (expanding.length === 0) return true;
  if (base === "git") return gitMessageExpansionsOnly(args, vars) || gitReadOnlyLoopExpansions(args, vars);
  if (!READ_ONLY_DATA_TOOLS.has(base) && !(valueClass && base === "mkdir")) return false;
  for (const a of args) {
    if (a.startsWith("--pre") || a.startsWith("--hostname-bin") || a.startsWith("--compress-program") || (base === "printf" && a === "-v")) {
      return false;
    }
  }
  for (const a of expanding) {
    const names = wordExpansionNames(a);
    if (names === null) return false;
    for (const name of names) {
      const bound = vars.has(name);
      if (name === CMD_SUBST_VAR) {
        if (bound || (!VALUE_ONLY_TOOLS.has(base) && !(valueClass && acceptsValueClassArg(base)))) return false;
      } else if (bound) {
        if (vars.get(name) !== true || !READ_ONLY_DATA_TOOLS.has(base)) return false;
      } else if (!HARMLESS_ENV_VARS.has(name) || !READ_ONLY_DATA_TOOLS.has(base)) {
        return false;
      }
    }
  }
  return true;
}

setLiteralJudge({
  opaqueCore: (inner) => opaqueShellCore(inner, "", false, false),
  isSecret: (p) => isSecretPath(p) || isSensitiveExternalPath(p),
  isProtected: isProtectedPath,
  shellWords,
});
