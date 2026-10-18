// .opencode/plugins/hoxi-permissions.js
const DESTRUCTIVE_RE =
  /\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*\/etc\/|git\s+push\b[^\n;&|]*(--force\b|-f\b|--force-with-lease\b)|git\s+reset\b[^\n;&|]*--hard\b|git\s+clean\b[^\n;&|]*\s-[a-z]*f/i;

const SAFE_COMMANDS = [
  /^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd)\b/,
  /^git\s+(status|diff|log|show|branch|rev-parse|worktree)\b/,
  /^go\s+(test|vet|build|list|env|version)\b/,
  /^npm\s+(test|run\s+build|run\s+lint)\b/,
  /^pnpm\s+(test|build|lint)\b/,
  /^cargo\s+(check|test)\b/,
  /^(pytest|tsc)\b/,
  /^node\s+-v\b/,
];

const READ_PERMISSIONS = new Set(["read", "glob", "grep", "list", "lsp"]);
const SHELL_SPLIT_RE = /\s*(?:&&|\|\||;|\|(?!\|)|(?<!>)&(?!>)|\n|\$\(|`)\s*/;

function pushText(out, value) {
  if (typeof value === "string") {
    const text = value.trim();
    if (text) out.push(text);
    return;
  }
  if (Array.isArray(value)) {
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
  return String(command || "")
    .split(SHELL_SPLIT_RE)
    .map((segment) => segment.replace(/\)\s*$/, "").trim())
    .filter(Boolean);
}

function isSafeSegment(segment) {
  if (/\s-(exec|delete|ok|fprint)\b/.test(segment)) return false;
  if (/[>|]\s*\/etc\//.test(segment)) return false;
  if (/`|\$\(/.test(segment)) return false;
  const withoutHarmlessRedirect = segment
    .replace(/\s+\d*>>?\s*\/dev\/null\b/g, "")
    .replace(/\s+2>&1\b/g, "")
    .trim();
  if (/[<>]/.test(withoutHarmlessRedirect)) return false;
  return SAFE_COMMANDS.some((re) => re.test(withoutHarmlessRedirect));
}

export function decidePermission(input) {
  const permission = String(input?.permission || input?.type || "").toLowerCase();
  const commands = commandCandidates(input);
  const blob = [permission, ...commands].join("\n");
  if (DESTRUCTIVE_RE.test(blob)) return "ask";
  if (READ_PERMISSIONS.has(permission)) return "allow";
  if (commands.length === 0) return "ask";
  const segments = commands.flatMap(splitCommandChain);
  if (segments.length === 0 || !segments.every(isSafeSegment)) return "ask";
  return "allow";
}

export const HoxiPermissionsPlugin = async () => {
  return {
    "permission.ask": async (input, output) => {
      output.status = decidePermission(input);
    },
  };
};

export default HoxiPermissionsPlugin;
