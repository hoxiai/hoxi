// Shared by the OpenCode plugin, desktop approval modes, and the Go port in
// internal/runner/permission_policy.go. Keep both ports and
// .opencode/lib/permission-policy.cases.json in sync.
const DESTRUCTIVE_RE =
  /\b(rm|rmdir|unlink|shred|truncate|srm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|\bkill\s+(?:-[A-Za-z0-9]+\s+)*(?:[A-Za-z0-9]+\s+)?[-?\d%]|\bpkill\b|[>|]\s*(?:\/etc\/|(?:\~|\$HOME|\$\{HOME\})\/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))|(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:ssh|aws|gnupg|kube)\/|(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:netrc|npmrc|git-credentials)(?:$|[\s;&|'"*?])|\.claude\/\.credentials\.json|\.docker\/config\.json|(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.en(?:v(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?|stag(?:e|ing)|test(?:ing)?|ci|preview|secret\w*|key\w*|private\w*|bak|backup)(?:\.local)?)?|\?|\*)(?:$|[\s;&|'"*?])|(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.envrc(?:$|[\s;&|'"*?])|\bfind\b[^\n;&|]*\s-(delete|exec\w*|ok\w*|fprint\w*|fls)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b|\b(?:npm|pnpm|yarn|cargo)\s+publish\b|\bterraform\s+(?:destroy|apply)\b|\bkubectl\s+delete\b|\bdocker\s+(?:system\s+prune|rm|rmi)\b/i;

// Match Git global options, option abbreviations (`--forc`, `--har`), combined
// short flags (`-uf`), `+refspec` force pushes and `:refspec` deletions.
const DESTRUCTIVE_GIT_RE =
  /\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S)|reset\b[^\n;&|]*\s--h(?:a\S*)?(?:[=\s]|$)|clean\b[^\n;&|]*\s-[a-zA-Z]*f|checkout\b[^\n;&|]*(?:\s--(?:\s|$)|\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s\.(?:[;&|]|\s|\/|$))|restore\b|stash\s+(?:drop|clear)\b|switch\b[^\n;&|]*(?:\s-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--di(?:s(?:c\S*)?)?(?:[=\s]|$)))/;

const TRAILING_PAREN_RE = /\)\s*$/;
const DOLLAR_EXPAND_RE = /[A-Za-z0-9_{('"@*#?!$-]/;
const REDIRECT_WORD_RE = /^(?:\d*|&)>>?$/;
const DEV_NULL_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$/;
const DUP_STREAM_WORD_RE = /^\d*>&[12]$/;
const CD_SEGMENT_RE = /^cd[ \t]+([A-Za-z0-9_./@%+,:][A-Za-z0-9_./@%+,:-]*)$/;
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
const TOOLCHAIN_BASES = new Set(["go", "npm", "pnpm", "yarn", "cargo", "pytest", "tsc", "npx", "node", "git"]);
const SYSTEM_TOOL_DIRS = new Set([
  "/usr/local/go/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
]);

export const READ_PERMISSIONS = new Set(["read", "glob", "grep", "list", "lsp", "todoread"]);

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
const SANDBOX_WRAP_RE = /^'?\/usr\/bin\/sandbox-exec'?\s+-f\s+(?:'[^']*'|\S+)\s+--\s+\/bin\/sh\s+-c\s+'((?:[^']|'\\'')*)'$/;

export function unwrapSandboxCommand(command) {
  const match = SANDBOX_WRAP_RE.exec(String(command || "").trim());
  return match ? match[1].replaceAll("'\\''", "'") : command;
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
  return out.map(unwrapSandboxCommand);
}

function isWordBoundary(c) {
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

function shellWords(s) {
  const words = [];
  let cur = "";
  let inWord = false;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
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

function isWorktreeCD(segment, worktree) {
  const wt = String(worktree || "").trim();
  if (!wt.startsWith("/")) return false;
  const m = CD_SEGMENT_RE.exec(String(segment || "").trim());
  if (!m) return false;
  const raw = m[1];
  const target = cleanPosixPath(raw.startsWith("/") ? raw : `${wt}/${raw}`);
  const base = cleanPosixPath(wt);
  return target === base || target.startsWith(base === "/" ? "/" : `${base}/`);
}

function normalizeAbsoluteTool(word) {
  if (!word.startsWith("/")) return word;
  const slash = word.lastIndexOf("/");
  const dir = cleanPosixPath(word.slice(0, slash) || "/");
  const base = word.slice(slash + 1);
  if (!TOOLCHAIN_BASES.has(base) || !SYSTEM_TOOL_DIRS.has(dir)) return word;
  return base;
}

export function splitCommandChain(command) {
  const s = String(command || "");
  const segments = [];
  let cur = "";
  let quote = "";
  const flush = () => {
    const seg = cur.replace(TRAILING_PAREN_RE, "").trim();
    if (seg) segments.push(seg);
    cur = "";
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
        flush();
        i++;
      } else if (c === "`") {
        flush();
      } else {
        cur += c;
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      cur += c;
    } else if ((c === "&" && next === "&") || (c === "|" && next === "|") || (c === "$" && next === "(")) {
      flush();
      i++;
    } else if (c === ";" || c === "\n" || c === "`" || c === "|") {
      flush();
    } else if (c === "&" && (i === 0 || s[i - 1] !== ">") && next !== ">") {
      flush();
    } else {
      cur += c;
    }
  }
  flush();
  return segments;
}

function stripNullRedirects(words) {
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
  if (cmd === "node") {
    return args.length === 1 && args[0] === "-v";
  }
  return false;
}

function isSafeSegment(segment) {
  const words = stripNullRedirects(shellWords(segment));
  if (!words || words.length === 0) return false;
  if (words.some((w) => FIND_ACTION_RE.test(w))) return false;
  words[0] = normalizeAbsoluteTool(words[0]);
  return isSafeWords(words);
}

function isSafeCommand(command, worktree) {
  if (hasUnsafeShellSyntax(command)) return false;
  let segments = splitCommandChain(command);
  if (segments.length > 0 && isWorktreeCD(segments[0], worktree)) {
    segments = segments.slice(1);
  }
  return segments.length > 0 && segments.every(isSafeSegment);
}

const EDIT_PERMISSIONS = new Set([
  "edit",
  "write",
  "patch",
  "multiedit",
  "apply_patch",
]);

const PROTECTED_PATH_SEGMENTS = new Set([
  ".git",
  ".agents",
  ".opencode",
  ".claude",
  ".hoxi",
  ".envrc",
]);

// Shell/credential dotfiles and OS-level execution hooks are the only
// cross-directory targets that stay manual: everything else outside the
// session directory (sibling workspace folders, SDK caches) is routine.
const SENSITIVE_BASENAMES = new Set([
  ".zshrc", ".zshenv", ".zprofile", ".zlogin",
  ".bashrc", ".bash_profile", ".bash_login", ".profile",
  ".gitconfig", ".git-credentials", ".netrc", ".npmrc",
  ".curlrc", ".wgetrc", ".envrc", "authorized_keys",
]);

const SENSITIVE_PATH_PREFIXES = [
  "/etc/",
  "/private/etc/",
  "/library/launchagents/",
  "/library/launchdaemons/",
  "/library/startupitems/",
];

const SENSITIVE_PATH_FRAGMENTS = [
  "/.config/opencode/",
  "/.config/hoxi/",
  "/.config/claude/",
  "/.ssh/",
  "/.aws/",
  "/.gnupg/",
  "/.kube/",
  "/library/keychains/",
];

function isSensitiveExternalPath(rawPath) {
  if (typeof rawPath !== "string") return false;
  const trimmed = rawPath.trim();
  if (!trimmed) return false;
  const candidates = [trimmed];
  const stripped = trimmed.replace(/\/?\*+$/, "");
  if (stripped && stripped !== trimmed) candidates.push(stripped);
  for (const cand of candidates) {
    if (isProtectedPath(cand)) return true;
    const slash = cand.replaceAll("\\", "/").toLowerCase();
    const base = slash.slice(slash.lastIndexOf("/") + 1);
    if (SENSITIVE_BASENAMES.has(base)) return true;
    if (SENSITIVE_PATH_PREFIXES.some((p) => slash.startsWith(p))) return true;
    if (SENSITIVE_PATH_FRAGMENTS.some((p) => slash.includes(p))) return true;
    if (matchesDestructive(`bash\n${cand}`)) return true;
  }
  return false;
}

export function isProtectedPath(rawPath) {
  if (typeof rawPath !== "string" || !rawPath.trim()) return false;
  const slash = rawPath.trim().replaceAll("\\", "/").toLowerCase();
  if (slash === ".vscode/settings.json" || slash.endsWith("/.vscode/settings.json")) {
    return true;
  }
  const parts = slash.split("/").filter(Boolean);
  return parts.some((p) => PROTECTED_PATH_SEGMENTS.has(p));
}

function matchesDestructive(blob) {
  return DESTRUCTIVE_RE.test(blob) || DESTRUCTIVE_GIT_RE.test(blob);
}

const TOOL_ARG_PATH_KEYS = [
  "Path", "AbsolutePath", "TargetFile", "target_file", "file_path", "filePath",
  "filepath", "File", "file", "path", "target", "DirectoryPath", "directory_path",
  "SearchPath", "search_path", "SearchDirectory", "search_directory",
  "Directory", "directory", "NotebookPath", "notebook_path",
];

function extractToolArgPaths(rawArgs) {
  let obj = rawArgs;
  if (typeof rawArgs === "string" && rawArgs.trim().startsWith("{")) {
    try {
      obj = JSON.parse(rawArgs);
    } catch {
      return [];
    }
  }
  if (!obj || typeof obj !== "object") return [];
  const out = [];
  for (const key of TOOL_ARG_PATH_KEYS) {
    if (typeof obj[key] === "string" && obj[key].trim()) {
      out.push(obj[key].trim());
    }
  }
  return out;
}

export function externalDirectoryTargets(input) {
  const meta = input?.metadata || {};
  const out = [];
  for (const value of [
    ...(Array.isArray(input?.patterns) ? input.patterns : []),
    input?.pattern,
    meta.filepath,
    meta.filePath,
    meta.path,
    meta.target,
    ...(Array.isArray(meta.directories) ? meta.directories : []),
    ...(Array.isArray(meta.files)
      ? meta.files.map((f) => f?.filePath || f?.filepath || f?.file || f?.path || "")
      : []),
    ...extractToolArgPaths(meta.args),
  ]) {
    if (typeof value === "string" && value.trim()) out.push(value.trim());
  }
  return out;
}

export function isDestructivePermissionRequest(input) {
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (permission === "doom_loop") return true;
  if (permission === "external_directory") {
    // OpenCode raises this for every read/write outside the session directory,
    // including sibling folders the user already registered as a workspace.
    // Only credential/execution-controlling targets keep asking.
    const targets = externalDirectoryTargets(input);
    return targets.length === 0 || targets.some(isSensitiveExternalPath);
  }
  if (input?.metadata?.destructive === true) return true;
  if (input?.metadata?.externalDirectory === true) {
    const targets = externalDirectoryTargets(input);
    if (targets.length === 0 || targets.some(isSensitiveExternalPath)) return true;
  }
  if (READ_PERMISSIONS.has(permission)) return false;
  if (EDIT_PERMISSIONS.has(permission) || permission === "tool") {
    const toolName = String(input?.metadata?.tool || input?.patterns?.[0] || "").trim().toLowerCase();
    if (toolName === "unsandboxed") return true;
    const candidates = [
      ...(Array.isArray(input?.patterns) ? input.patterns : []),
      input?.pattern,
      input?.metadata?.filepath,
      input?.metadata?.filePath,
      input?.metadata?.path,
      input?.metadata?.target,
      ...(Array.isArray(input?.metadata?.files)
        ? input.metadata.files.map((f) => f?.filePath || f?.filepath || f?.file || f?.path || "")
        : []),
      ...extractToolArgPaths(input?.metadata?.args),
    ];
    if (candidates.some(isProtectedPath)) return true;
    for (const cand of candidates) {
      if (typeof cand === "string" && cand.trim() && matchesDestructive(`bash\n${cand.trim()}`)) {
        return true;
      }
    }
  }
  const blob = [permission, ...commandCandidates(input)].join("\n");
  const deobfuscated = blob.replace(/[\\'"\[\]]/g, "");
  return matchesDestructive(blob) || matchesDestructive(deobfuscated);
}

export function isSafeCommandPermissionRequest(input, options) {
  if (isDestructivePermissionRequest(input)) return false;
  const worktree = options?.worktree || input?.metadata?.worktree || "";
  const candidates = commandCandidates(input);
  return candidates.length > 0 && candidates.every((cmd) => isSafeCommand(cmd, worktree));
}

export function decidePermission(input, options) {
  if (isDestructivePermissionRequest(input)) return "ask";
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (READ_PERMISSIONS.has(permission)) return "allow";
  return isSafeCommandPermissionRequest(input, options) ? "allow" : "ask";
}
