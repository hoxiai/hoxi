// Path, secret, and session-record risk helpers shared by permission-policy.js
// and OpenCode tool wrappers. Mirrors internal/runner/external_path_risk.go and
// the path predicates in internal/runner/approval_decision.go.

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

function isRoutineHoxiSubpath(slash) {
  return (
    slash.includes("/.hoxi/skills/") || slash.includes("/.hoxi/attachments/") || slash.includes("/.agents/skills/") ||
    slash.startsWith(".hoxi/skills/") || slash.startsWith(".hoxi/attachments/") || slash.startsWith(".agents/skills/")
  );
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
