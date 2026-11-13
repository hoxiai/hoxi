// The approval decision itself lives in Go (internal/runner/approval_decision.go):
// it answers every runtime's requests, with or without a window open. The
// desktop uses this port only to explain a docked request (e.g. the
// destructive warning on the approval card). Keep it identical to the Go
// port: .opencode/lib/permission-policy.cases.json covers commands and
// .opencode/lib/permission-request.cases.json covers whole requests.
import { isSafeKillProbeArgs } from "./script-interpreter.js";
import { setLiteralJudge } from "./shell-constants.js";
import { destructiveScanForms } from "./shell-substitution.js";
import { hasProtectedGitOutput } from "./git-output.js";
import { literalCDTarget } from "./shell-paths.js";
import {
  EDIT_PERMISSIONS,
  OWN_SESSION_EDIT_TOOLS,
  OWN_SESSION_READ_TOOLS,
  READ_PERMISSIONS,
  externalDirectoryTargets,
  extractToolArgPaths,
  isOwnSessionRecordRead,
  isProtectedPath,
  isProtectedShellReadPath,
  isReadOnlyTaskStatusRequest,
  isSecretPath,
  isSensitiveExternalPath,
  isSensitiveExternalReadPath,
  sensitiveOutsideOwnSession,
} from "./external-path-risk.js";

export {
  READ_PERMISSIONS,
  externalDirectoryTargets,
  isOwnSessionRecordRead,
  isProtectedPath,
  isProtectedShellReadPath,
  isSecretPath,
  isSensitiveExternalPath,
};

// A .env-style secret file or an .envrc under any directory (`apay/.env`).
// The unattended secret scan uses the same two; mirrors envFilePattern in
// internal/runner/permission_policy.go.
export const ENV_FILE_PATTERN = String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.en(?:v(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?|stag(?:e|ing)|test(?:ing)?|ci|preview|secret\w*|key\w*|private\w*|bak|backup)(?:\.local)?)?|\?|\*)(?:$|[\s;&|)'"*?])`;
export const ENVRC_FILE_PATTERN = String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.envrc(?:$|[\s;&|)'"*?])`;

const DESTRUCTIVE_RE = new RegExp(
  [
    // Core destructive / privilege / disk / power commands (anchored to command/word boundaries, not mid-path dirs, hyphens, or source extensions)
    String.raw`(?:^|[\s;&|()<>\x60'"=:\/{},$])(?:mkfs(?:\.[a-z0-9]+)?|(?:rm|rmdir|unlink|shred|truncate|srm|sudo|chmod|chown|dd|diskutil|killall|reboot|shutdown)(?:\.(?:exe|cmd|bat))?)(?:$|[\s;&|()<>\x60'"\\{},$])`,
    // kill (excluding pure `kill -0 <pid>` liveness probes) and pkill
    String.raw`\bkill\s+(?:(?:(?:-0\s+)*(?:-(?:[1-9a-mo-rt-z][a-z0-9]*|[sn][a-z0-9]+|0[a-z0-9]+|-)\s+|-[sn]\s+(?:[1-9a-z][a-z0-9]*|0[a-z0-9]+)\s+))+[-?\d%]|(?:-[1-9]\d*|[1-9?%]|0(?:\s|$)))`,
    String.raw`\bpkill\b`,
    // Redirections or pipes into /etc/ or user shell/credential dotfiles
    String.raw`[>|]\s*(?:\/etc\/|(?:\~|\$HOME|\$\{HOME\})\/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))`,
    // Writes/copies/in-place edits targeting execution-controlling repo directories
    String.raw`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*\/)?\.(?:git\/(?:hooks\/|config\b)|opencode\/|claude\/|agents\/|hoxi\/skills\/|vscode\/settings\.json\b|gemini\/(?:settings|config\/hooks)\.json\b)`,
    // Credential directories and standalone credential files
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:ssh|aws|gnupg|kube)\/`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:netrc|npmrc|git-credentials|credentials\.json)(?:$|[\s;&|)'"*?])`,
    String.raw`\.claude\/\.credentials\.json`,
    String.raw`\.hoxi\/(?:models|auth|tunnel(?:-dev)?|cloudflared|pairing|relay-keys|work-apps|approval-policy|hoxi)\.(?:json|db)`,
    String.raw`\.docker\/config\.json`,
    // .env / .env.* / .envrc files
    ENV_FILE_PATTERN,
    ENVRC_FILE_PATTERN,
    // Destructive find flags, curl/wget pipe-to-shell, publish, cloud/container teardown, and local Hoxi control APIs
    String.raw`\bfind\b[^\n;&|]*\s-(delete|exec\w*|ok\w*|fprint\w*|fls)\b`,
    String.raw`\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b`,
    String.raw`\b(?:npm|pnpm|yarn|cargo)\s+publish\b`,
    String.raw`\bterraform\s+(?:destroy|apply)\b`,
    String.raw`\bkubectl\s+delete\b`,
    String.raw`\bdocker\s+(?:system\s+prune|rm|rmi)\b`,
    String.raw`\/api\/hoxi\/(?:tools\/run|computer\/(?:action|grant|pause|stop))\b`,
  ].join("|"),
  "i",
);

// Match Git global options, option abbreviations (`--forc`, `--har`), combined
// short flags (`-uf`), `+refspec` force pushes and `:refspec` deletions.
const DESTRUCTIVE_GIT_RE = new RegExp(
  String.raw`\bgit\b[^\n;&|]*?\b(?:` +
    [
      String.raw`branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))`,
      String.raw`worktree\s+(?:remove|prune)\b`,
      String.raw`push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S)`,
      String.raw`reset\b[^\n;&|]*\s--h(?:a\S*)?(?:[=\s]|$)`,
      String.raw`checkout\b[^\n;&|]*(?:\s--(?:\s|$)|\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s\.(?:[;&|]|\s|\/|$))`,
      String.raw`stash\s+(?:drop|clear)\b`,
      String.raw`switch\b[^\n;&|]*(?:\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--di(?:s(?:c\S*)?)?(?:[=\s]|$))`,
    ].join("|") +
    String.raw`)`,
);

const DESTRUCTIVE_GIT_CLEAN_RESTORE_RE = /\bgit\b[^\n;&|()]*?\b(?:clean\b[^\n;&|()]*\s-[a-zA-Z]*f[^\n;&|()]*|restore\b[^\n;&|()]*)/g;
const SAFE_GIT_RESTORE_STAGED_RE = /^git(?:\s+(?:-C\s+[^\s<>$`]+|--no-pager|--bare|--git-dir=[^\s<>$`]+|--work-tree=[^\s<>$`]+))*\s+restore((?:\s+(?:-[Sq]+|--staged|--quiet))+)(?:\s+--)?(?:\s+[^\s<>$`-][^\s<>$`]*)+(?:\s+(?:2?>\s*\/dev\/null|2>&1))*\s*$/;
const SAFE_GIT_RESTORE_HAS_STAGED_RE = /(?:\s-[qS]*S[qS]*|\s--staged)(?:\s|$)/;
const SAFE_GIT_CLEAN_DRY_RUN_RE = /^git(?:\s+(?:-C\s+[^\s<>$`]+|--no-pager))*\s+clean((?:\s+(?:-[fdxXnq]+|--force|--dry-run|--quiet))+)(?:\s+--)?(?:\s+[^\s<>$`-][^\s<>$`]*)*(?:\s+(?:2?>\s*\/dev\/null|2>&1))*\s*$/;
const SAFE_GIT_CLEAN_HAS_DRY_RUN_RE = /(?:\s-[fdxXnq]*n[fdxXnq]*|\s--dry-run)(?:\s|$)/;

const TRAILING_PAREN_RE = /\)\s*$/;
export const DOLLAR_EXPAND_RE = /[A-Za-z0-9_{('"@*#?!$-]/;
const REDIRECT_WORD_RE = /^(?:\d*|&)>>?$/;
const DEV_NULL_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$/;
const DUP_STREAM_WORD_RE = /^\d*>&[12]$/;
export const CD_SEGMENT_RE = /^cd[ \t]+([A-Za-z0-9_./@%+,:][A-Za-z0-9_./@%+,:~-]*)$/;
const CD_NULL_REDIRECT_SUFFIX_RE = /(?:[ \t]+(?:(?:\d*|&)>>?[ \t]*\/dev\/null|\d*>&[12]))+$/;
export const stripCDNullRedirects = (s) => (s.startsWith("cd ") || s.startsWith("cd\t") ? s.replace(CD_NULL_REDIRECT_SUFFIX_RE, "") : s);
const FIND_ACTION_RE = /^-(exec\w*|ok\w*|delete|fprint\w*|fls)$/;
const FD_EXEC_RE = /^(-[^-]*[xX]|--exec)/;
const RG_EXEC_RE = /^--(pre|hostname-bin)/;
const PYTEST_WRITE_RE = /^--(ba|ju|result-?log|pas|deb)/;
const TSC_WRITE_RE = /^(-b$|--(out|declarationdir|tsbuildinfofile|generatetrace|generatecpuprofile|build))/i;
const GIT_OUTPUT_RE = /^--(o(u[^=]*)?|ex(t[^=]*)?)(=|$)/;
const NPM_SCRIPT_RE = /^(build|lint|check)(:[\w.:-]+)?$/;
const PNPM_SCRIPT_RE = /^(test|build|lint|check)(:[\w.:-]+)?$/;
const CARGO_UNSAFE_RE = /^(--config|-Z|--target-dir|--manifest-path)/;
const GIT_BRANCH_SHORT_RE = /^-(?:[arv]+)$/;
const GIT_BRANCH_LONG_RE = /^(?:--all|--remotes|--verbose|--show-current|--no-color)$/;

const SIMPLE_SAFE_TOOLS = new Set([
  "ls", "pwd", "cat", "head", "tail", "wc", "find", "grep", "rg", "fd",
  "lsof", "ps", "which", "whereis", "uname", "stat", "file", "du", "df", "pytest", "tsc",
]);
const GIT_READ_SUBCOMMANDS = new Set(["status", "diff", "log", "show", "rev-parse"]);
const GO_READ_SUBCOMMANDS = new Set(["test", "vet", "build", "list", "version", "env"]);
const GO_UNSAFE_FLAGS = new Set([
  "exec", "toolexec", "vettool", "o", "ldflags", "gccgoflags",
  "coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace", "outputdir",
]);
const GO_ENV_WRITE_FLAGS = new Set(["w", "u"]);
const HOXI_CODE_INDEX_SUBCOMMANDS = new Set(["search", "inspect", "callers", "callees", "impact", "context", "explore", "refresh", "status"]);
const HOXI_DOC_READ_SUBCOMMANDS = new Set(["inspect", "read"]);
const HOXI_ECOM_READ_SUBCOMMANDS = new Set(["profit", "listing-check", "ads-audit", "help"]);
const TOOLCHAIN_BASES = new Set(["go", "npm", "pnpm", "yarn", "cargo", "pytest", "tsc", "npx", "node", "git", "hoxi"]);
const SYSTEM_TOOL_DIRS = new Set([
  "/usr/local/go/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
]);

function pushText(out, value) {
  if (typeof value === "string") {
    const text = value.trim();
    if (text) out.push(text);
  } else if (Array.isArray(value)) {
    for (const item of value) pushText(out, item);
  }
}

// The Hoxi sandbox plugin rewrites bash commands into
// `'/usr/bin/sandbox-exec' -f '<profile>' -- /bin/sh -c '<command>'`; judge the
// wrapped command, not the wrapper.
const SANDBOX_WRAP_RE = /^(?:'[^']*\/sandbox-exec'|\/usr\/bin\/sandbox-exec|sandbox-exec)\s+-f\s+(?:'[^']*'|\S+)\s+--\s+(?:\/bin\/sh|sh)\s+-c\s+'((?:[^']|'\\'')*)'$/;
const SANDBOX_WRAP_PREFIX_RE = /^(?:'[^']*\/sandbox-exec'|\/usr\/bin\/sandbox-exec|sandbox-exec)\s+-f\s+(?:'[^']*'|\S+)\s+--\s+(?:\/bin\/sh|sh)\s+-c\s+'/;
const OPEN_CODE_STOP_TRAP_USR1_TEXT = `trap 'printf "\\n[Command stopped by user]\\n"; exit 130' USR1`;
const OPEN_CODE_STOP_TRAP_TEXT = `${OPEN_CODE_STOP_TRAP_USR1_TEXT}; trap : EXIT`;
const OPEN_CODE_STOP_TRAP_USR1_RE = String.raw`trap 'printf "\\n\[Command stopped by user\]\\n"; exit 130' USR1`;
const OPEN_CODE_STOP_TRAP_RE = `${OPEN_CODE_STOP_TRAP_USR1_RE}; trap : EXIT`;
const OPEN_CODE_STOP_MARKER_RE = new RegExp(
  String.raw`^(?:[ \t]*(?:# hoxi-stop:[^\r\n]*(?:\r?\n(?:[ \t]*(?:` +
    OPEN_CODE_STOP_TRAP_RE +
    String.raw`|` +
    OPEN_CODE_STOP_TRAP_USR1_RE +
    String.raw`|trap : (?:EXIT|\S+))(?:[ \t]*\r?\n|$))?|$)|(?:` +
    OPEN_CODE_STOP_TRAP_RE +
    String.raw`|` +
    OPEN_CODE_STOP_TRAP_USR1_RE +
    String.raw`|trap : EXIT)(?:[ \t]*\r?\n|[ \t]+|$)))+`,
);

export function stripOpenCodeStopMarkerText(raw) {
  const s = String(raw ?? "");
  if (!s.includes("# hoxi-stop:") && !s.includes("trap : EXIT") && !s.includes(OPEN_CODE_STOP_TRAP_USR1_TEXT)) return s;
  return s.replace(OPEN_CODE_STOP_MARKER_RE, "");
}

export function unwrapSandboxCommand(command) {
  let cur = String(command ?? "");
  for (let i = 0; i < 16; i++) {
    let next = stripOpenCodeStopMarkerText(cur);
    const trimmed = next.trim();
    const match = SANDBOX_WRAP_RE.exec(trimmed);
    if (match) {
      next = stripOpenCodeStopMarkerText(match[1].replaceAll("'\\''", "'"));
    } else if (trimmed.endsWith("…")) {
      const prefix = SANDBOX_WRAP_PREFIX_RE.exec(trimmed);
      if (prefix) {
        next = stripOpenCodeStopMarkerText(trimmed.slice(prefix[0].length).replaceAll("'\\''", "'"));
      }
    }
    if (next === cur) break;
    cur = next;
  }
  return cur;
}

export function commandCandidates(input) {
  const out = [];
  pushText(out, input?.patterns);
  pushText(out, input?.pattern);
  const meta = input?.metadata || {};
  pushText(out, meta.command);
  pushText(out, meta.cmd);
  pushText(out, meta.commands);
  if (out.length === 0) pushText(out, input?.title);
  return out.map((cmd) => unwrapSandboxCommand(cmd).trim()).filter(Boolean);
}

export function isWordBoundary(c) {
  return " \t\r\n;&|()<>".includes(c);
}

export function hasUnsafeShellSyntax(command) {
  const s = String(command || "");
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const prev = i > 0 ? s[i - 1] : "";
    const next = i + 1 < s.length ? s[i + 1] : "";
    if (quote === "'") {
      if (c === "'") {
        if (s.startsWith("\\''", i + 1)) {
          i += 3;
          continue;
        }
        quote = "";
        if (next && !isWordBoundary(next)) return true;
      }
      continue;
    }
    if (c === "\\" || c === "`") return true;
    if (c === "$" && next && !(quote === '"' && next === '"') && DOLLAR_EXPAND_RE.test(next)) {
      return true;
    }
    if (quote === '"') {
      if (c === '"') {
        quote = "";
        if (next && !isWordBoundary(next)) return true;
      }
      continue;
    }
    if (c === "*" || c === "?" || c === "[") return true;
    if (c === "'" || c === '"') {
      if (prev && !isWordBoundary(prev) && prev !== "=") return true;
      quote = c;
    }
  }
  return quote !== "";
}

export function shellWords(s) {
  const words = [];
  let cur = "";
  let inWord = false;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (quote === "'" && c === "'" && s.startsWith("\\''", i + 1)) {
        cur += "'";
        i += 3;
        continue;
      }
      if (c === quote) {
        quote = "";
      } else {
        cur += c;
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      inWord = true;
    } else if (c === " " || c === "\t" || c === "\r" || c === "\n") {
      if (inWord) {
        words.push(cur);
        cur = "";
        inWord = false;
      }
    } else {
      cur += c;
      inWord = true;
    }
  }
  if (inWord) words.push(cur);
  return words;
}

function cleanPosixPath(p) {
  const abs = p.startsWith("/");
  const parts = [];
  for (const seg of p.split("/")) {
    if (!seg || seg === ".") continue;
    if (seg === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") {
        parts.pop();
      } else if (!abs) {
        parts.push("..");
      }
    } else {
      parts.push(seg);
    }
  }
  const joined = parts.join("/");
  return abs ? "/" + joined : joined || ".";
}

export function isWorktreeCD(segment, worktree) {
  const wt = String(worktree || "").trim();
  if (!wt.startsWith("/")) return false;
  const raw = literalCDTarget(stripCDNullRedirects(String(segment || "").trim()));
  if (raw === null) return false;
  const target = cleanPosixPath(raw.startsWith("/") ? raw : `${wt}/${raw}`);
  const base = cleanPosixPath(wt);
  return !isSensitiveExternalPath(target) && !isSecretPath(target) && !isProtectedShellReadPath(target) &&
    (target === base || target.startsWith(base === "/" ? "/" : `${base}/`));
}

export function normalizeAbsoluteTool(word) {
  if (!word.startsWith("/")) return word;
  const slash = word.lastIndexOf("/");
  const dir = cleanPosixPath(word.slice(0, slash) || "/");
  const base = word.slice(slash + 1);
  if (!TOOLCHAIN_BASES.has(base) || !SYSTEM_TOOL_DIRS.has(dir)) return word;
  return base;
}

export function splitCommandChain(command) {
  return splitCommandChainWithPipes(command).segments;
}

export function splitCommandChainWithPipes(command) {
  const s = stripOpenCodeStopMarkerText(command);
  const segments = [];
  const pipedIn = [];
  let cur = "";
  let quote = "";
  let lastPipe = false;
  const flush = (nextPipe = false) => {
    const seg = cur.replace(TRAILING_PAREN_RE, "").trim();
    if (seg) {
      segments.push(seg);
      pipedIn.push(lastPipe);
    }
    cur = "";
    lastPipe = nextPipe;
  };
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const next = i + 1 < s.length ? s[i + 1] : "";
    if (quote === "'") {
      if (c === "'") quote = "";
      cur += c;
      continue;
    }
    if (c === "\\" && i + 1 < s.length) {
      cur += c + next;
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === '"') {
        quote = "";
        cur += c;
      } else if (c === "$" && next === "(") {
        flush(false);
        i++;
      } else if (c === "`") {
        flush(false);
      } else {
        cur += c;
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      cur += c;
    } else if ((c === "&" && next === "&") || (c === "|" && next === "|") || (c === "$" && next === "(")) {
      flush(false);
      i++;
    } else if (c === "|") {
      flush(true);
    } else if (c === ";" || c === "\n" || c === "`") {
      flush(false);
    } else if (c === "&" && (i === 0 || s[i - 1] !== ">") && next !== ">") {
      flush(false);
    } else {
      cur += c;
    }
  }
  flush(false);
  return { segments, pipedIn };
}

export function stripNullRedirects(words) {
  const out = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    if (DEV_NULL_WORD_RE.test(w) || DUP_STREAM_WORD_RE.test(w)) continue;
    if (REDIRECT_WORD_RE.test(w) && i + 1 < words.length && words[i + 1] === "/dev/null") {
      i++;
      continue;
    }
    if (/[<>]/.test(w)) return null;
    out.push(w);
  }
  return out;
}

function isReadOnlyGitBranch(args) {
  if (args.length === 0 || args[0] !== "branch") return false;
  let listing = false;
  for (const arg of args.slice(1)) {
    if (arg === "--list") {
      listing = true;
    } else if (GIT_BRANCH_SHORT_RE.test(arg) || GIT_BRANCH_LONG_RE.test(arg)) {
      continue;
    } else if (!listing || arg.startsWith("-")) {
      return false;
    }
  }
  return true;
}

function noneMatch(args, re) {
  return !args.some((a) => re.test(a));
}

function toolArgsSafe(cmd, args) {
  if (cmd === "fd") return noneMatch(args, FD_EXEC_RE);
  if (cmd === "rg") return noneMatch(args, RG_EXEC_RE);
  if (cmd === "pytest") return noneMatch(args, PYTEST_WRITE_RE);
  if (cmd === "tsc") return noneMatch(args, TSC_WRITE_RE);
  return true;
}

function goArgsSafe(sub, args) {
  const blocked = sub === "env" ? GO_ENV_WRITE_FLAGS : GO_UNSAFE_FLAGS;
  for (const a of args) {
    if (!a.startsWith("-")) continue;
    const name = a.replace(/^--?/, "").split("=", 1)[0];
    if (blocked.has(name)) return false;
  }
  return true;
}

function npmArgsSafe(args) {
  for (const a of args) {
    if (a === "--") return true;
    if (a.startsWith("-")) return false;
  }
  return true;
}

function isSafeWords(words) {
  const [cmd, ...args] = words;
  const arg = (i) => (i < args.length ? args[i] : "");
  if (SIMPLE_SAFE_TOOLS.has(cmd)) return toolArgsSafe(cmd, args);
  if (cmd === "git") {
    if (isReadOnlyGitBranch(args) || (arg(0) === "worktree" && arg(1) === "list")) return true;
    return GIT_READ_SUBCOMMANDS.has(arg(0)) && noneMatch(args.slice(1), GIT_OUTPUT_RE);
  }
  if (cmd === "go") {
    return GO_READ_SUBCOMMANDS.has(arg(0)) && goArgsSafe(arg(0), args.slice(1));
  }
  if (cmd === "npm") {
    if (arg(0) === "test") return npmArgsSafe(args.slice(1));
    return arg(0) === "run" && NPM_SCRIPT_RE.test(arg(1)) && npmArgsSafe(args.slice(2));
  }
  if (cmd === "pnpm") {
    return PNPM_SCRIPT_RE.test(arg(0)) && npmArgsSafe(args.slice(1));
  }
  if (cmd === "cargo") {
    return (arg(0) === "check" || arg(0) === "test") && noneMatch(args.slice(1), CARGO_UNSAFE_RE);
  }
  if (cmd === "npx") {
    return arg(0) === "tsc" && toolArgsSafe("tsc", args.slice(1));
  }
  if (cmd === "node") return args.length === 1 && ["-v", "--version", "-h", "--help"].includes(args[0]);
  if (cmd === "kill") return isSafeKillProbeArgs(args);
  if (cmd === "hoxi") {
    if (arg(0) === "doc") {
      return HOXI_DOC_READ_SUBCOMMANDS.has(arg(1)) && args.length >= 3 && hoxiCodeIndexArgsSafe(args.slice(2));
    }
    if (arg(0) === "ecom") {
      return args.length >= 2 && HOXI_ECOM_READ_SUBCOMMANDS.has(arg(1)) && hoxiEcomArgsSafe(args.slice(2));
    }
    return (arg(0) === "code-index" || arg(0) === "codeindex") && HOXI_CODE_INDEX_SUBCOMMANDS.has(arg(1)) && hoxiCodeIndexArgsSafe(args.slice(2));
  }
  return false;
}

function hoxiEcomArgsSafe(args) {
  for (let i = 0; i < args.length; i++) {
    const a = String(args[i] || "").trim();
    if (a === "-o" || a === "--out" || a === "-out" || a.startsWith("-o=") || a.startsWith("--out=") || a.startsWith("-out=")) {
      return false;
    }
  }
  return hoxiCodeIndexArgsSafe(args);
}

function hoxiCodeIndexArgsSafe(args) {
  for (let i = 0; i < args.length; i++) {
    const a = String(args[i] || "").trim();
    if (!a) continue;
    if (a === "-dir" || a === "--dir" || a.startsWith("-dir=") || a.startsWith("--dir=")) {
      return false;
    }
    let val = a;
    if (a.startsWith("-")) {
      const eq = a.indexOf("=");
      if (eq < 0) continue;
      val = a.slice(eq + 1);
    }
    const parts = val.split(/[\\/]+/).filter(Boolean);
    if (parts.includes("..") || isProtectedPath(val) || isSecretPath(val) || isSensitiveExternalPath(val)) {
      return false;
    }
  }
  return true;
}

function isSafeSegment(segment) {
  const words = stripNullRedirects(shellWords(segment));
  if (!words || words.length === 0) return false;
  if (words.some((w) => FIND_ACTION_RE.test(w))) return false;
  words[0] = normalizeAbsoluteTool(words[0]);
  return isSafeWords(words);
}

function isSafeCommand(command, worktree) {
  command = unwrapSandboxCommand(command);
  if (hasUnsafeShellSyntax(command)) return false;
  let segments = splitCommandChain(command);
  if (segments.length > 0 && isWorktreeCD(segments[0], worktree)) {
    segments = segments.slice(1);
  }
  return segments.length > 0 && segments.every(isSafeSegment);
}

function isSafeGitCleanOrRestoreMatch(m) {
  const restoreSub = SAFE_GIT_RESTORE_STAGED_RE.exec(m);
  if (restoreSub) return SAFE_GIT_RESTORE_HAS_STAGED_RE.test(restoreSub[1]);
  const cleanSub = SAFE_GIT_CLEAN_DRY_RUN_RE.exec(m);
  if (cleanSub) return SAFE_GIT_CLEAN_HAS_DRY_RUN_RE.test(cleanSub[1]);
  return false;
}

function hasDestructiveGitCleanOrRestore(blob) {
  for (const [m] of blob.matchAll(DESTRUCTIVE_GIT_CLEAN_RESTORE_RE)) {
    if (!isSafeGitCleanOrRestoreMatch(m)) return true;
  }
  return false;
}

function matchesDestructive(blob) {
  return DESTRUCTIVE_RE.test(blob) || DESTRUCTIVE_GIT_RE.test(blob) || hasDestructiveGitCleanOrRestore(blob) || hasProtectedGitOutput(blob, splitCommandChain, shellWords);
}

function hoxiToolsAction(input) {
  const perm = String(input?.permission || input?.type || "").trim().toLowerCase();
  const meta = input?.metadata || {};
  const toolName = String(meta.tool || input?.tool?.name || (Array.isArray(input?.patterns) ? input.patterns[0] : "") || "").trim().toLowerCase();
  if (perm !== "hoxi_tools" && toolName !== "hoxi_tools" && !toolName.startsWith("hoxi_tools_")) {
    return { isHoxiTools: false, action: "" };
  }
  if (toolName.startsWith("hoxi_tools_")) return { isHoxiTools: true, action: toolName.slice("hoxi_tools_".length) };
  let argsAction = meta.args && typeof meta.args === "object" ? String(meta.args.action || "") : "";
  if (!argsAction && typeof meta.args === "string" && meta.args.trim().startsWith("{")) {
    try { argsAction = String(JSON.parse(meta.args)?.action || ""); } catch {}
  }
  const act = String(meta.action || argsAction || "").trim().toLowerCase();
  if (act) return { isHoxiTools: true, action: act };
  for (const pat of [...(Array.isArray(input?.patterns) ? input.patterns : []), input?.pattern]) {
    if (typeof pat !== "string" || !pat.trim()) continue;
    const first = pat.trim().toLowerCase().split(/\s+/)[0];
    if (first === "discover" || first === "inspect" || first === "run") return { isHoxiTools: true, action: first };
  }
  return { isHoxiTools: true, action: "run" };
}

export function isDestructivePermissionRequest(input) {
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (permission === "hoxi_computer" || String(input?.metadata?.tool || "").toLowerCase() === "hoxi_computer") return true;
  if (permission === "doom_loop") return true;
  const hoxiTools = hoxiToolsAction(input);
  if (hoxiTools.isHoxiTools) return hoxiTools.action !== "discover" && hoxiTools.action !== "inspect";
  if (permission === "external_directory") {
    // OpenCode raises this for every read/write outside the session directory,
    // including sibling folders the user already registered as a workspace.
    // Only credential/execution-controlling targets keep asking. A read of this
    // session's own runner record is not one of them.
    const targets = externalDirectoryTargets(input);
    return targets.length === 0 || targets.some((t) => isSensitiveExternalPath(t) && !isOwnSessionRecordRead(input, t));
  }
  if (input?.metadata?.destructive === true) return true;
  const toolName = String(input?.metadata?.tool || input?.patterns?.[0] || "").trim().toLowerCase();
  const readOnlyTool = permission === "tool" && !OWN_SESSION_EDIT_TOOLS.has(toolName) && (OWN_SESSION_READ_TOOLS.has(toolName) || isReadOnlyTaskStatusRequest(input));
  if (input?.metadata?.externalDirectory === true) {
    const targets = externalDirectoryTargets(input);
    const isSens = readOnlyTool ? isSensitiveExternalReadPath : isSensitiveExternalPath;
    if (targets.length === 0 || targets.some((t) => isSens(t) && !isOwnSessionRecordRead(input, t))) return true;
  }
  if (READ_PERMISSIONS.has(permission)) {
    // Search patterns are not paths; only a file read can expose a secret.
    if (permission !== "read") return false;
    const meta = input?.metadata || {};
    const readPaths = [...(Array.isArray(input?.patterns) ? input.patterns : []), input?.pattern, meta.filepath, meta.filePath, meta.path];
    return readPaths.some((p) => isSecretPath(p) && !isOwnSessionRecordRead(input, p));
  }
  if (EDIT_PERMISSIONS.has(permission) || permission === "tool") {
    if (toolName === "unsandboxed") return true;
    const meta = input?.metadata || {};
    const candidates = [
      ...(permission === "tool" ? [] : Array.isArray(input?.patterns) ? input.patterns : []),
      ...(permission === "tool" ? [] : [input?.pattern]),
      meta.filepath, meta.filePath, meta.path, meta.target,
      // apply_patch patterns name only the source of a move; movePath is where it lands.
      ...(Array.isArray(meta.files) ? meta.files.flatMap((f) => [f?.filePath || f?.filepath || f?.file || f?.path || "", f?.movePath || ""]) : []),
      ...extractToolArgPaths(meta.args),
    ];
    if (sensitiveOutsideOwnSession(input, candidates, readOnlyTool)) return true;
    if (EDIT_PERMISSIONS.has(permission)) return false;
    if (typeof meta.command !== "string" && typeof meta.cmd !== "string" && !Array.isArray(meta.commands)) return false;
  }
  const blob = [permission, ...commandCandidates(input).flatMap(destructiveScanForms)].join("\n");
  const deobfuscated = blob.replace(/[\\'"\[\]]/g, "");
  return matchesDestructive(blob) || matchesDestructive(deobfuscated);
}

export function isSafeCommandPermissionRequest(input, options) {
  if (isDestructivePermissionRequest(input)) return false;
  const meta = input?.metadata || {};
  const worktree = options?.worktree || meta.worktree || "";
  let candidates = commandCandidates(input);
  const whole = typeof meta.command === "string" ? unwrapSandboxCommand(meta.command).trim() : "";
  if (whole) {
    candidates = [whole, ...candidates.filter((piece) => piece && !whole.includes(piece))];
  }
  return candidates.length > 0 && candidates.every((cmd) => isSafeCommand(cmd, worktree));
}

export function decidePermission(input, options) {
  if (isDestructivePermissionRequest(input)) return "ask";
  const hoxiTools = hoxiToolsAction(input);
  if (hoxiTools.isHoxiTools && (hoxiTools.action === "discover" || hoxiTools.action === "inspect")) return "allow";
  if (isReadOnlyTaskStatusRequest(input)) return "allow";
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (READ_PERMISSIONS.has(permission) || permission === "external_directory") return "allow";
  return isSafeCommandPermissionRequest(input, options) ? "allow" : "ask";
}

// Literal substitutions scanned from this module fail closed on value-class
// $(...) until opaque-command.js installs the real core. Shell words and the
// secret check are enough for `D=.env; cat "$D"`.
setLiteralJudge({
  opaqueCore: () => true,
  isSecret: (p) => isSecretPath(p) || isSensitiveExternalPath(p),
  isProtected: isProtectedShellReadPath,
  normalizeAbsoluteTool,
  shellWords,
});
