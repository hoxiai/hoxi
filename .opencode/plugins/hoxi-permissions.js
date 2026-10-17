// .opencode/plugins/hoxi-permissions.js
const DESTRUCTIVE_RE =
  /\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*\/etc\/|git\s+(push\s+--force|reset\s+--hard|clean\s+-f)/i;

const SAFE_PREFIX_RE =
  /^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd|git\s+(status|diff|log|show|branch|rev-parse|worktree)|go\s+(test|vet|build|list|env|version)|npm\s+(test|run\s+build|run\s+lint)|pnpm\s+(test|build|lint)|cargo\s+(check|test)|pytest|tsc|node\s+-v)\b/;

export const HoxiPermissionsPlugin = async () => {
  return {
    "permission.ask": async (input, output) => {
      const joined = [
        input.permission || "",
        ...(Array.isArray(input.patterns) ? input.patterns : []),
        JSON.stringify(input.metadata || {}),
      ]
        .join(" ")
        .trim();

      // 1. Destructive primitives must ALWAYS pause for interactive user confirmation
      if (DESTRUCTIVE_RE.test(joined)) {
        output.status = "ask";
        return;
      }

      // 2. Read-only operations and safe local build/test commands auto-pass
      if (
        input.permission === "read" ||
        input.permission === "glob" ||
        input.permission === "grep" ||
        input.permission === "list" ||
        input.permission === "lsp" ||
        SAFE_PREFIX_RE.test(joined)
      ) {
        output.status = "allow";
        return;
      }

      // 3. Default to asking user for unrecognized external commands
      output.status = "ask";
    },
  };
};

export default HoxiPermissionsPlugin;
