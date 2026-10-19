// Shared by the OpenCode plugin and the desktop approval modes.
const DESTRUCTIVE_RE =
  /\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*\/etc\/|\bfind\b[^\n;&|]*\s-(delete|exec|execdir|ok|okdir)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b/i;

// Match Git global options too, such as `git -C repo branch -D name`.
const DESTRUCTIVE_GIT_RE =
  /\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:--force\b|-f\b|--force-with-lease\b)|reset\b[^\n;&|]*--hard\b|clean\b[^\n;&|]*\s-[a-zA-Z]*f)/;

const SAFE_COMMANDS = [
  /^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd|lsof|ps|which|whereis|uname|stat|file|du|df)\b/,
  /^git\s+(status|diff|log|show|rev-parse)\b/,
  /^git\s+worktree\s+list(?:\s|$)/,
  /^go\s+(test|vet|build|list|env|version)\b/,
  /^npm\s+(test|run\s+build|run\s+lint|run\s+check)\b/,
  /^pnpm\s+(test|build|lint|check)\b/,
  /^cargo\s+(check|test)\b/,
  /^(pytest|tsc|npx\s+tsc)\b/,
  /^node\s+-v\b/,
];

export const READ_PERMISSIONS = new Set(["read", "glob", "grep", "list", "lsp", "todoread"]);
const SHELL_SPLIT_RE = /\s*(?:&&|\|\||;|\|(?!\|)|(?<!>)&(?!>)|\n|\$\(|`)\s*/;

function pushText(out, value) {
  if (typeof value === "string") {
    const text = value.trim();
    if (text) out.push(text);
  } else if (Array.isArray(value)) {
    for (const item of value) pushText(out, item);
  }
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
  return out;
}

export function splitCommandChain(command) {
  const s = String(command || "");
  const segments = [];
  let cur = "";
  let quote = "";
  const flush = () => {
    const seg = cur.replace(/\)\s*$/, "").trim();
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

function isReadOnlyGitBranch(command) {
  if (!/^git\s+branch(?:\s|$)/.test(command)) return false;
  const args = command.trim().split(/\s+/).slice(2);
  let listing = false;
  for (const arg of args) {
    if (arg === "--list") {
      listing = true;
    } else if (/^-(?:[arv]+)$/.test(arg) ||
      /^(?:--all|--remotes|--verbose|--show-current|--no-color)$/.test(arg)) {
      continue;
    } else if (!listing || arg.startsWith("-")) {
      return false;
    }
  }
  return true;
}

function isSafeSegment(segment) {
  if (/\s-(exec|execdir|delete|ok|okdir|fprint)\b/.test(segment)) return false;
  if (/`|\$\(/.test(segment)) return false;
  const command = segment
    .replace(/\s+\d*>>?\s*\/dev\/null\b/g, "")
    .replace(/\s+2>&1\b/g, "")
    .trim();
  if (/[<>]/.test(command)) return false;
  return isReadOnlyGitBranch(command) || SAFE_COMMANDS.some((re) => re.test(command));
}

export function isDestructivePermissionRequest(input) {
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (permission === "doom_loop") return true;
  if (READ_PERMISSIONS.has(permission)) return false;
  const blob = [permission, ...commandCandidates(input)].join("\n");
  return DESTRUCTIVE_RE.test(blob) || DESTRUCTIVE_GIT_RE.test(blob);
}

export function isSafeCommandPermissionRequest(input) {
  if (isDestructivePermissionRequest(input)) return false;
  const segments = commandCandidates(input).flatMap(splitCommandChain);
  return segments.length > 0 && segments.every(isSafeSegment);
}

export function decidePermission(input) {
  if (isDestructivePermissionRequest(input)) return "ask";
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  if (READ_PERMISSIONS.has(permission)) return "allow";
  return isSafeCommandPermissionRequest(input) ? "allow" : "ask";
}
