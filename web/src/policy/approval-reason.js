// Human-readable explanation of why a permission request triggered manual
// confirmation. Pure policy classifier used only by the desktop UI to show a
// concise reason banner on ApprovalGateCard; never weakens Go gate decisions
// and never surfaces secret values or raw file paths in `detail`.
import {
  EDIT_PERMISSIONS,
  READ_PERMISSIONS,
  extractToolArgPaths,
  externalDirectoryTargets,
  isProtectedPath,
  isSecretPath,
} from "./external-path-risk.js";
import { hasProtectedGitOutput } from "./git-output.js";
import { isOpaquePermissionRequest } from "./opaque-command.js";
import {
  ENVRC_FILE_PATTERN,
  ENV_FILE_PATTERN,
  KEY_FILE_PATTERN,
  commandCandidates,
  isDestructivePermissionRequest,
  normalizeAbsoluteTool,
  shellWords,
  splitCommandChain,
  unwrapSandboxCommand,
} from "./permission-policy.js";
import { inlineScriptTool } from "./script-interpreter.js";
import { destructiveScanForms } from "./shell-substitution.js";

const SECRET_CMD_RE = new RegExp(
  [
    ENV_FILE_PATTERN,
    ENVRC_FILE_PATTERN,
    KEY_FILE_PATTERN,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:ssh|aws|gnupg|kube|codex)\/`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?(?:\.config\/opencode|\.local\/share\/opencode)\/`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:netrc|npmrc|git-credentials|credentials\.json)(?:$|[\s;&|)'"*?])`,
    String.raw`\.claude\/\.credentials\.json`,
    String.raw`\.hoxi\/(?:models|media-roles|intake|auth|tunnel(?:-dev)?|cloudflared|pairing|relay-keys|work-apps|dataprofiles|approval-policy|hoxi)\.(?:json|db)`,
    String.raw`\.docker\/config\.json`,
  ].join("|"),
  "i"
);

const PROTECTED_WRITE_RE = new RegExp(
  [
    String.raw`[>|]\s*(?:\/etc\/|(?:\~|\$HOME|\$\{HOME\})\/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))`,
    String.raw`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*\/)?\.(?:git\/(?:hooks\/|config\b)|opencode\/|claude\/|agents\/|hoxi\/skills\/|vscode\/settings\.json\b|gemini\/(?:settings|config\/hooks)\.json\b)`,
    String.raw`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*\/)?hoxi\.json(?:$|[\s;&|)'"*?])`,
  ].join("|"),
  "i"
);

const PIPE_TO_SHELL_RE = /\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b/i;

const PUBLISH_OR_FORCE_RE = new RegExp(
  [
    String.raw`\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S))`,
    String.raw`\b(?:npm|pnpm|yarn|cargo)\s+publish\b`,
    String.raw`\bterraform\s+(?:destroy|apply)\b`,
    String.raw`\bkubectl\s+delete\b`,
    String.raw`\bdocker\s+(?:system\s+prune|rm|rmi)\b`,
    String.raw`\/api\/hoxi\/(?:tools\/run|dataprofiles\b|intake\b|computer\/(?:action|grant|pause|stop))\b`,
  ].join("|"),
  "i"
);

const PRIV_OR_PROCESS_RE =
  /(?:^|[\s;&|()<>\x60'"=:/{},$])(mkfs(?:\.[a-z0-9]+)?|(?:sudo|chmod|chown|dd|diskutil|killall|reboot|shutdown)(?:\.(?:exe|cmd|bat))?)(?:$|[\s;&|()<>\x60'"\\{},$])/i;
const KILL_OR_PKILL_RE =
  /\b(pkill)\b|\b(kill)\s+(?:(?:(?:-0\s+)*(?:-(?:[1-9a-mo-rt-z][a-z0-9]*|[sn][a-z0-9]+|0[a-z0-9]+|-)\s+|-[sn]\s+(?:[1-9a-z][a-z0-9]*|0[a-z0-9]+)\s+))+[-?\d%]|(?:-[1-9]\d*|[1-9?%]|0(?:\s|$)))/i;

const DELETE_OR_DISCARD_RE = new RegExp(
  [
    String.raw`(?:^|[\s;&|()<>\x60'"=:\/{},$])(rm|rmdir|unlink|shred|truncate|srm)(?:\.(?:exe|cmd|bat))?(?:$|[\s;&|()<>\x60'"\\{},$])`,
    String.raw`\b(find)\b[^\n;&|]*\s-(delete|exec\w*|ok\w*|fprint\w*|fls)\b`,
    String.raw`\b(git)\b[^\n;&|]*?\b(reset\b[^\n;&|]*\s--h|checkout\b[^\n;&|]*(?:\s--(?:\s|$)|\s-[a-zA-Z]*f|\s\.)|stash\s+(?:drop|clear)\b|switch\b[^\n;&|]*(?:\s-[a-zA-Z]*f|\s--f|\s--di)|clean\b[^\n;&|()]*\s-[a-zA-Z]*f|restore\b)`,
  ].join("|"),
  "i"
);

const DEOBFUSCATE_RE = /[\\'"[\]]/g;
const ENV_ASSIGN_RE = /^[A-Za-z_][A-Za-z0-9_]*=/;

function optsOutOfSandbox(command) {
  const lines = String(command ?? "").split(/\r?\n/);
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) continue;
    if (line === "# hoxi:unsandboxed" || line.startsWith("# hoxi:unsandboxed ")) return true;
    if (line.startsWith("#")) continue;
    return false;
  }
  return false;
}

function detectInlineInterpreter(command) {
  const unwrapped = unwrapSandboxCommand(command);
  const hasHeredoc = unwrapped.includes("<<");
  for (const seg of splitCommandChain(unwrapped)) {
    const words = shellWords(seg);
    let idx = 0;
    while (idx < words.length && ENV_ASSIGN_RE.test(words[idx])) idx++;
    if (idx >= words.length) continue;
    const norm = normalizeAbsoluteTool(words[idx]);
    const base = norm.includes("/") ? norm.slice(norm.lastIndexOf("/") + 1) : norm;
    const family = inlineScriptTool(base);
    if (!family) continue;
    const args = words.slice(idx + 1);
    const hasInlineArg = args.some(
      (a) =>
        a === "-" ||
        a === "-c" ||
        a === "-e" ||
        a === "--eval" ||
        a === "-p" ||
        a === "--print" ||
        a === "-r" ||
        a.startsWith("-c") ||
        a.startsWith("-e") ||
        a.startsWith("--eval=") ||
        a.startsWith("--print=") ||
        a.startsWith("-r")
    );
    if (hasInlineArg || (hasHeredoc && (args.length === 0 || args.every((a) => a.startsWith("-"))))) {
      return family;
    }
  }
  return "";
}

function classifyDestructiveBash(candidates) {
  const forms = [];
  for (const cmd of candidates) {
    for (const f of destructiveScanForms(cmd)) {
      forms.push(f);
      forms.push(f.replace(DEOBFUSCATE_RE, ""));
    }
  }
  const blob = forms.join("\n");
  if (SECRET_CMD_RE.test(blob)) return { code: "secret_read", severity: "danger" };
  if (PROTECTED_WRITE_RE.test(blob) || candidates.some((c) => hasProtectedGitOutput(c, splitCommandChain, shellWords))) {
    return { code: "protected_path", severity: "danger" };
  }
  if (PIPE_TO_SHELL_RE.test(blob)) return { code: "pipe_to_shell", severity: "danger" };
  if (PUBLISH_OR_FORCE_RE.test(blob)) {
    const detail = /\bgit\b/i.test(blob) ? "git" : /\bpublish\b/i.test(blob) ? "publish" : undefined;
    return { code: "publish_or_force_push", severity: "danger", detail };
  }
  const killMatch = KILL_OR_PKILL_RE.exec(blob);
  if (killMatch) {
    return { code: "privilege_or_process", severity: "danger", detail: (killMatch[1] || killMatch[2] || "kill").toLowerCase() };
  }
  const privMatch = PRIV_OR_PROCESS_RE.exec(blob);
  if (privMatch) {
    return { code: "privilege_or_process", severity: "danger", detail: privMatch[1].toLowerCase() };
  }
  const delMatch = DELETE_OR_DISCARD_RE.exec(blob);
  if (delMatch) {
    const verb = (delMatch[1] || delMatch[2] || delMatch[3] || "rm").toLowerCase();
    return { code: "destructive_delete", severity: "danger", detail: verb };
  }
  return { code: "high_risk", severity: "danger" };
}

function collectRequestPaths(input) {
  const meta = input?.metadata || {};
  const paths = [];
  const push = (v) => {
    if (typeof v === "string" && v.trim()) paths.push(v.trim());
    else if (Array.isArray(v)) v.forEach(push);
  };
  push(input?.patterns);
  push(input?.pattern);
  push(meta.filepath);
  push(meta.filePath);
  push(meta.path);
  push(meta.target);
  if (Array.isArray(meta.files)) {
    for (const f of meta.files) {
      if (f && typeof f === "object") {
        push(f.filePath || f.filepath || f.file || f.path);
        push(f.movePath);
      }
    }
  }
  push(extractToolArgPaths(meta.args));
  return paths;
}

/**
 * Classifies why `input` requires user confirmation.
 * @returns {{ code: string, severity: "danger" | "warn" | "info", detail?: string }}
 */
export function explainPermissionRequest(input, options = {}) {
  const perm = String(input?.permission || input?.type || "").toLowerCase().trim();
  const meta = input?.metadata || {};
  const tool = String(meta.tool || input?.tool?.name || "").toLowerCase().trim();

  if (perm === "doom_loop") return { code: "doom_loop", severity: "danger" };
  if (perm === "hoxi_computer" || tool === "hoxi_computer") return { code: "computer_use", severity: "danger" };
  if (tool === "unsandboxed") return { code: "unsandboxed", severity: "danger", detail: "unsandboxed" };

  const isBash =
    perm === "bash" ||
    perm === "shell" ||
    perm === "run_command" ||
    typeof meta.command === "string" ||
    typeof meta.cmd === "string" ||
    Array.isArray(meta.commands);
  const candidates = isBash ? commandCandidates(input) : [];
  const destructive = isDestructivePermissionRequest(input);

  if (destructive) {
    if (perm === "hoxi_tools" || tool === "hoxi_tools" || tool.startsWith("hoxi_tools_")) {
      return { code: "external_tool", severity: "danger", detail: "hoxi_tools" };
    }
    if (perm === "hoxi_data" || tool === "hoxi_data") {
      return { code: "external_tool", severity: "danger", detail: "hoxi_data" };
    }
    if (perm === "external_directory") {
      const targets = externalDirectoryTargets(input);
      if (targets.some((p) => isSecretPath(p))) return { code: "secret_read", severity: "danger" };
      return { code: "external_path", severity: "danger" };
    }
    if (READ_PERMISSIONS.has(perm)) {
      return { code: "secret_read", severity: "danger" };
    }
    if (EDIT_PERMISSIONS.has(perm) || (perm === "tool" && !isBash)) {
      const paths = collectRequestPaths(input);
      if (paths.some((p) => isSecretPath(p))) return { code: "secret_read", severity: "danger" };
      if (paths.some((p) => isProtectedPath(p))) return { code: "protected_path", severity: "danger" };
      if (meta.externalDirectory) return { code: "external_path", severity: "danger" };
      return { code: "high_risk", severity: "danger" };
    }
    if (isBash && candidates.length > 0) {
      return classifyDestructiveBash(candidates);
    }
    return { code: "high_risk", severity: "danger" };
  }

  if (isBash && candidates.some(optsOutOfSandbox)) {
    return { code: "unsandboxed", severity: "warn", detail: "# hoxi:unsandboxed" };
  }

  if (isBash && isOpaquePermissionRequest(input, { worktree: options.worktree, readOnlyInlineScripts: true })) {
    for (const cmd of candidates) {
      const interp = detectInlineInterpreter(cmd);
      if (interp) return { code: "inline_script", severity: "warn", detail: interp };
    }
    return { code: "opaque_shell", severity: "warn" };
  }

  return { code: "mode_policy", severity: "info" };
}
