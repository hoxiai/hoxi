// Path, secret, and session-record risk helpers shared by permission-policy.js
// and OpenCode tool wrappers. Mirrors internal/runner/external_path_risk.go and
// the path predicates in internal/runner/approval_decision.go.
import { unwrapCommandPrefixes } from "./command-wrappers.js";

export const READ_PERMISSIONS = new Set(["read", "glob", "grep", "list", "lsp", "todoread"]);

export const EDIT_PERMISSIONS = new Set([
  "edit", "write", "patch", "multiedit", "apply_patch", "notebookedit", "notebook_edit",
  "write_to_file", "write_file", "create_file", "edit_file", "replace", "replace_file_content",
  "multi_replace_file_content", "sed_file",
]);

const PROTECTED_PATH_SEGMENTS = new Set([".git", ".agents", ".opencode", ".claude", ".hoxi", ".envrc"]);

// Shell/credential dotfiles and OS-level execution hooks are the only
// cross-directory targets that stay manual: everything else outside the
// session directory (sibling workspace folders, SDK caches) is routine.
const SENSITIVE_BASENAMES = new Set([
  ".zshrc", ".zshenv", ".zprofile", ".zlogin",
  ".bashrc", ".bash_profile", ".bash_login", ".profile",
  ".gitconfig", ".git-credentials", ".netrc", ".npmrc",
  ".pypirc", ".curlrc", ".wgetrc", ".envrc", "authorized_keys",
  "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", ".credentials.json",
  "oauth_creds.json", "jetski-standalone-oauth-token",
]);

const SENSITIVE_PATH_PREFIXES = ["/etc/", "/private/etc/", "/root/", "/var/root/", "/private/var/root/"];

const SENSITIVE_PATH_FRAGMENTS = [
  "/.config/opencode/", "/.config/hoxi/", "/.config/claude/", "/.config/gh/", "/.config/gcloud/",
  "/.hoxi/", "/.claude/", "/.ssh/", "/.aws/", "/.gnupg/", "/.kube/", "/.docker/", "/.azure/",
  "/library/keychains/", "/library/launchagents/", "/library/launchdaemons/", "/library/startupitems/",
];

const SENSITIVE_ENV_BASENAME_RE =
  /^\.en(?:v(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?|stag(?:e|ing)|test(?:ing)?|ci|preview|secret\w*|key\w*|private\w*|bak|backup)(?:\.local)?)?|\?|\*)$/i;

const CLAUDE_SKILL_CREDENTIAL_NAMES = new Set(["auth.json", "credentials.json", "token.json", "tokens.json", "secrets.json", "oauth.json"]);
const CLAUDE_SKILL_SHELL_READ_TOOLS = new Set(["cat", "head", "tail", "grep", "rg", "sed", "awk", "jq", "diff", "cmp", "stat", "file", "ls", "find", "wc", "nl", "cut"]);
const CLAUDE_SKILL_READ_TOOL_DIRS = new Set(["/usr/local/go/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"]);

function isRoutineHoxiSubpath(slash) {
  return (
    slash.includes("/.hoxi/skills/") || slash.includes("/.hoxi/attachments/") || slash.includes("/.agents/skills/") ||
    slash.startsWith(".hoxi/skills/") || slash.startsWith(".hoxi/attachments/") || slash.startsWith(".agents/skills/")
  );
}

// This port is lexical; Go additionally resolves links and checks the concrete
// destination stays inside the installed skill root. A wildcard is range-only.
export function isClaudeSkillReadPath(rawPath, directoryScope = false) {
  if (typeof rawPath !== "string") return false;
  const slash = rawPath.trim().replaceAll("\\", "/");
  if (claudeSkillPathHasAmbiguousInput(rawPath) || !slash || /[\0\r\n$`]/.test(slash) || (!slash.startsWith("/") && !/^[a-z]:\//i.test(slash))) return false;
  const parts = slash.replace(/\/+$/, "").split("/").filter((p) => p !== "" && p !== ".");
  if (parts.includes("..")) return false;
  if (directoryScope && ["*", "**"].includes(parts[parts.length - 1])) parts.pop();
  if (parts.some((p) => /[*?\[\]]/.test(p))) return false;
  const lower = parts.map((p) => p.toLowerCase());
  const index = lower.findIndex((p, i) => p === ".claude" && lower[i + 1] === "skills");
  if (index < 0) return false;
  const parent = (slash.startsWith("/") ? "/" : "") + parts.slice(0, index).join("/");
  if (isProtectedPath(parent) || isSensitiveExternalPath(parent) || lower.slice(0, index).some((p) => PROTECTED_PATH_SEGMENTS.has(p))) return false;
  const parentSlash = "/" + parent.toLowerCase().replace(/^\/+/, "") + "/";
  if (SENSITIVE_PATH_FRAGMENTS.some((p) => parentSlash.includes(p)) || SENSITIVE_PATH_PREFIXES.some((p) => parentSlash.startsWith(p))) return false;
  const suffix = lower.slice(index + 2);
  if (suffix.some((p) => PROTECTED_PATH_SEGMENTS.has(p) || SENSITIVE_BASENAMES.has(p) || CLAUDE_SKILL_CREDENTIAL_NAMES.has(p) || SENSITIVE_ENV_BASENAME_RE.test(p) ||
    /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|^id_[a-z0-9_.-]+)$/i.test(p))) return false;
  if (SENSITIVE_PATH_FRAGMENTS.some((p) => ("/" + suffix.join("/") + "/").includes(p)) || isProtectedPath(suffix.join("/"))) return false;
  return true;
}

// A bare external_directory request grants scope only; write/execute requests
// still pass their own tool gate, and cannot claim the read-only exemption here.
export function isClaudeSkillReadRequest(input, target) {
  const perm = String(input?.permission || input?.type || "").trim().toLowerCase();
  const meta = input?.metadata || {};
  let tool = [meta.tool, input?.tool?.name].find((v) => typeof v === "string" && v.trim()) || "";
  if (!tool && perm === "tool") tool = input?.patterns?.[0] || "";
  tool = String(tool).trim().toLowerCase();
  if (meta.destructive === true || typeof meta.command === "string" || typeof meta.cmd === "string" || Array.isArray(meta.commands) ||
    OWN_SESSION_EDIT_TOOLS.has(perm) || OWN_SESSION_EDIT_TOOLS.has(tool)) return false;
  const readTool = READ_PERMISSIONS.has(tool) || OWN_SESSION_READ_TOOLS.has(tool);
  if (perm === "external_directory") return (!tool || readTool) && isClaudeSkillReadPath(target, true);
  return (READ_PERMISSIONS.has(perm) || (perm === "tool" && readTool)) && isClaudeSkillReadPath(target);
}

export function hasUnsafeClaudeSkillShellAccess(command, splitCommandChain, shellWords) {
  const keywords = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);
  const isSkillPath = (arg) => (arg.startsWith("/") || arg.startsWith("~/") || /^[a-z]:[\\/]/i.test(arg)) &&
    (arg.replaceAll("\\", "/").toLowerCase() + "/").includes("/.claude/skills/");
  for (const segment of splitCommandChain(command)) {
    let words = shellWords(segment);
    while (words.length && (keywords.has(words[0]) || /^[A-Za-z_][A-Za-z0-9_]*=/.test(words[0]))) words = words.slice(1);
    const parsed = unwrapCommandPrefixes(words);
    if (!parsed || parsed.lookupOnly || !parsed.words.length) continue;
    words = parsed.words;
    const base = words[0].slice(words[0].lastIndexOf("/") + 1);
    if (isSkillPath(words[0])) return true;
    const readTool = CLAUDE_SKILL_SHELL_READ_TOOLS.has(base) &&
      (!words[0].includes("/") || CLAUDE_SKILL_READ_TOOL_DIRS.has(words[0].slice(0, words[0].lastIndexOf("/"))));
    for (let arg of words.slice(1)) {
      if (arg.startsWith("-") && arg.includes("=")) arg = arg.slice(arg.indexOf("=") + 1);
      if (isSkillPath(arg) && (!readTool || !isClaudeSkillReadPath(arg))) return true;
    }
  }
  return false;
}

export function claudeSkillPathHasAmbiguousInput(raw) {
  if (typeof raw !== "string" || !("/" + raw.replaceAll("\\", "/").toLowerCase() + "/").includes("/.claude/skills/")) return false;
  const wrongSeparator = raw.includes("\\") && !/^[a-z]:[\\/]/i.test(raw) && !raw.startsWith("\\\\");
  return raw !== raw.trim() || wrongSeparator;
}

export function hasNonCanonicalClaudeSkillPath(input) {
  const check = (v) => {
    if (typeof v === "string") return claudeSkillPathHasAmbiguousInput(v);
    if (Array.isArray(v)) return v.some(check);
    if (v && typeof v === "object") return ["movePath", ...TOOL_ARG_PATH_KEYS].some((key) => check(v[key]));
    return false;
  };
  const perm = String(input?.permission || input?.type || "").toLowerCase(), meta = input?.metadata || {};
  if (["read", "external_directory"].includes(perm) && [input?.patterns, input?.pattern, input?.always].some(check)) return true;
  if (["filepath", "filePath", "path", "target", "parentDir", "dir", "directories", "files"].some((key) => check(meta[key]))) return true;
  let args = meta.args;
  if (typeof args === "string") { try { args = JSON.parse(args); } catch {} }
  return check(args);
}

// Reading these can expose credentials; mirrors isSecretFilePath in
// internal/runner/external_path_risk.go and OpenCode's own `*.env` read rule.
export function isSecretPath(rawPath) {
  if (typeof rawPath !== "string") return false;
  const rawSlash = rawPath.trim().replaceAll("\\", "/").toLowerCase().replace(/\/+$/, "");
  const slash = stripWorktreePrefix(rawSlash).replace(/\/+$/, "");
  if (!slash) return false;
  const base = slash.slice(slash.lastIndexOf("/") + 1);
  if (SENSITIVE_BASENAMES.has(base) || SENSITIVE_ENV_BASENAME_RE.test(base)) return true;
  if (isClaudeSkillReadPath(rawPath)) return false;
  if (isRoutineHoxiSubpath(slash)) return false;
  const normalized = slash.replace(/^\/+/, "");
  return SENSITIVE_PATH_FRAGMENTS.some((p) => `/${normalized}/`.includes(p));
}

function stripWorktreePrefix(slash) {
  const marker = "/.hoxi/worktrees/";
  const idx = slash.lastIndexOf(marker);
  if (idx !== -1) {
    const rest = slash.slice(idx + marker.length);
    const nextSlash = rest.indexOf("/");
    return nextSlash === -1 ? "" : rest.slice(nextSlash + 1);
  }
  if (slash.startsWith(".hoxi/worktrees/")) {
    const rest = slash.slice(".hoxi/worktrees/".length);
    const nextSlash = rest.indexOf("/");
    return nextSlash === -1 ? "" : rest.slice(nextSlash + 1);
  }
  return slash;
}

export function isSensitiveExternalPath(rawPath) {
  return isSensitiveExternalPathWith(rawPath, true);
}

export function isSensitiveExternalReadPath(rawPath) {
  return isSensitiveExternalPathWith(rawPath, false);
}

function isSensitiveExternalPathWith(rawPath, checkProtected) {
  if (typeof rawPath !== "string") return false;
  if (!checkProtected && isClaudeSkillReadPath(rawPath)) return false;
  const trimmed = rawPath.trim();
  if (!trimmed) return false;
  const candidates = [trimmed];
  const stripped = trimmed.replace(/\/?\*+$/, "");
  if (stripped && stripped !== trimmed) candidates.push(stripped);
  for (const cand of candidates) {
    const rawSlash = cand.replaceAll("\\", "/").toLowerCase().replace(/\/+$/, "");
    const slash = stripWorktreePrefix(rawSlash).replace(/\/+$/, "") || "/";
    const slashDir = slash === "/" ? "/" : `${slash}/`;
    const base = slash.slice(slash.lastIndexOf("/") + 1);
    if (SENSITIVE_BASENAMES.has(base) || SENSITIVE_ENV_BASENAME_RE.test(base)) return true;
    if (isRoutineHoxiSubpath(slash)) continue;
    if (checkProtected && isProtectedPath(cand)) return true;
    if (SENSITIVE_PATH_PREFIXES.some((p) => slashDir.startsWith(p))) return true;
    const normalizedDir = `/${slashDir.replace(/^\/+/, "")}`;
    if (SENSITIVE_PATH_FRAGMENTS.some((p) => normalizedDir.includes(p))) return true;
  }
  return false;
}

export function isProtectedPath(rawPath) {
  if (typeof rawPath !== "string" || !rawPath.trim()) return false;
  const rawSlash = rawPath.trim().replaceAll("\\", "/").toLowerCase();
  const slash = stripWorktreePrefix(rawSlash);
  if (!slash) return false;
  if (slash.includes("/.hoxi/attachments/") || slash.startsWith(".hoxi/attachments/")) {
    return false;
  }
  if (
    slash === ".vscode/settings.json" ||
    slash.endsWith("/.vscode/settings.json") ||
    slash === ".gemini/settings.json" ||
    slash.endsWith("/.gemini/settings.json") ||
    slash === ".gemini/config/hooks.json" ||
    slash.endsWith("/.gemini/config/hooks.json")
  ) {
    return true;
  }
  const parts = slash.split("/").filter(Boolean);
  return parts.some((p) => PROTECTED_PATH_SEGMENTS.has(p));
}

export function isProtectedShellReadPath(rawPath) {
  if (typeof rawPath !== "string" || !rawPath.trim()) return false;
  if (isClaudeSkillReadPath(rawPath)) return false;
  const slash = stripWorktreePrefix(rawPath.trim().replaceAll("\\", "/").toLowerCase());
  return isRoutineHoxiSubpath(slash) ? false : isProtectedPath(rawPath);
}

const TOOL_ARG_PATH_KEYS = [
  "Path", "AbsolutePath", "absolute_path", "TargetFile", "target_file", "file_path", "filePath",
  "filepath", "File", "file", "path", "paths", "target", "DirectoryPath", "directory_path",
  "dir_path", "dirPath", "SearchPath", "search_path", "SearchDirectory", "search_directory",
  "Directory", "directory", "NotebookPath", "notebook_path",
];

export function extractToolArgPaths(rawArgs) {
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
    const val = obj[key];
    if (typeof val === "string" && val.trim()) {
      out.push(val.trim());
    } else if (Array.isArray(val)) {
      for (const item of val) {
        if (typeof item === "string" && item.trim()) {
          out.push(item.trim());
        }
      }
    }
  }
  return out;
}

export function externalDirectoryTargets(input) {
  const meta = input?.metadata || {};
  const out = [];
  for (const value of [
    ...(Array.isArray(input?.patterns) ? input.patterns : []),
    input?.pattern, ...(Array.isArray(input?.always) ? input.always : []),
    meta.filepath, meta.filePath, meta.path, meta.target, meta.parentDir, meta.dir,
    ...(Array.isArray(meta.directories) ? meta.directories : []),
    ...(Array.isArray(meta.files) ? meta.files.map((f) => f?.filePath || f?.filepath || f?.file || f?.path || "") : []),
    ...extractToolArgPaths(meta.args),
  ]) {
    if (typeof value === "string" && value.trim()) out.push(value.trim());
  }
  return out;
}

// Reads of the requesting session's own ~/.hoxi/sessions/<id>/ record.
// Edits stay sensitive. No session id, another session, or ~/.hoxi itself
// stays sensitive. Both the Hoxi id and the runtime ExternalID match.
export const OWN_SESSION_READ_TOOLS = new Set([
  "read", "glob", "grep", "list", "lsp", "todoread", "hoxi_code_index", "ls", "notebookread", "notebook_read",
  "read_file", "read_many_files", "view_file", "list_dir", "list_directory", "find_by_name", "grep_search",
  "search_file_content", "read_resource", "list_resources", "read_browser_page", "browser_get_dom",
  "browser_get_network_request", "browser_list_network_requests", "list_browser_pages",
  "capture_browser_console_logs", "capture_browser_screenshot", "list_permissions", "list_plugin_accounts",
  "search_marketplace", "command_status", "wait", "wait_5_seconds",
]);

export const OWN_SESSION_EDIT_TOOLS = new Set([
  ...EDIT_PERMISSIONS, "insert_edit_into_file", "str_replace_editor", "str_replace_based_edit_tool",
]);

export function isReadOnlyTaskStatusRequest(input) {
  const perm = String(input?.permission || input?.type || "").trim().toLowerCase();
  if (perm && perm !== "tool" && perm !== "manage_task" && perm !== "manage_subagents") return false;
  const meta = input?.metadata || {};
  const tool = [meta.tool, input?.tool?.name, perm, input?.patterns?.[0]]
    .map((v) => String(v || "").trim().toLowerCase())
    .find((v) => v === "manage_task" || v === "manage_subagents");
  if (!tool) return false;
  let argsObj = meta.args && typeof meta.args === "object" ? meta.args : null;
  if (!argsObj && typeof meta.args === "string" && meta.args.trim().startsWith("{")) {
    try { argsObj = JSON.parse(meta.args); } catch {}
  }
  let raw = String(meta.action ?? argsObj?.Action ?? argsObj?.action ?? "").trim();
  if (raw.length >= 2 && raw.startsWith('"') && raw.endsWith('"')) {
    try { raw = String(JSON.parse(raw)).trim(); } catch {}
  }
  const act = raw.toLowerCase();
  return tool === "manage_task" ? act === "status" || act === "list" : act === "list";
}

export function isOwnSessionRecordRead(input, rawPath) {
  if (!pathUnderOwnSession(rawPath, requestSessionIDs(input))) return false;
  const perm = String(input?.permission || input?.type || "").toLowerCase();
  const tool = String(input?.metadata?.tool || (Array.isArray(input?.patterns) ? input.patterns[0] : "") || "").trim().toLowerCase();
  if (OWN_SESSION_EDIT_TOOLS.has(perm) || OWN_SESSION_EDIT_TOOLS.has(tool)) return false;
  if (perm === "external_directory") return true;
  return perm === "read" || READ_PERMISSIONS.has(perm) || OWN_SESSION_READ_TOOLS.has(perm) || OWN_SESSION_READ_TOOLS.has(tool) || isReadOnlyTaskStatusRequest(input);
}

function requestSessionIDs(input) {
  const meta = input?.metadata || {};
  const ids = [];
  for (const v of [input?.sessionID, input?.externalID, meta.sessionID, meta.externalID, meta.externalId]) {
    const id = typeof v === "string" ? v.trim() : "";
    if (validSessionID(id) && !ids.includes(id)) ids.push(id);
  }
  return ids;
}

function validSessionID(id) {
  return id !== "" && id !== "." && id !== ".." && id.length <= 200 && !/[\\/:*?]/.test(id);
}

function pathUnderOwnSession(raw, ids) {
  return ids.length > 0 && sessionPathCandidates(raw).some((cand) => sessionPathMatches(cand, ids));
}

function sessionPathCandidates(raw) {
  if (typeof raw !== "string" || !raw.trim()) return [];
  const trimmed = raw.trim();
  const stripped = trimmed.replace(/\*+$/, "").replace(/\/+$/, "");
  return stripped && stripped !== trimmed ? [trimmed, stripped] : [trimmed];
}

function sessionPathMatches(raw, ids) {
  let slash = raw.trim().replaceAll("\\", "/");
  if (slash.startsWith("~/")) slash = `HOME/${slash.slice(2)}`;
  if (slash.includes("..")) return false;
  const parts = slash.split("/").filter((p) => p !== "" && p !== ".");
  for (let i = 0; i + 2 < parts.length; i++) {
    if (parts[i] === ".hoxi" && parts[i + 1] === "sessions" && ids.includes(parts[i + 2])) return true;
  }
  return false;
}

export function sensitiveOutsideOwnSession(input, paths, readOnly = false) {
  const isRisky = readOnly ? (p) => isSecretPath(p) || isSensitiveExternalReadPath(p) : (p) => isProtectedPath(p) || isSensitiveExternalPath(p);
  return paths.some((p) => isRisky(p) && !isOwnSessionRecordRead(input, p));
}
