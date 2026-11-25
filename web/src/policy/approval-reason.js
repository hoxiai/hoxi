// Human-readable explanation of why a permission request triggered manual
// confirmation. Pure policy classifier used only by the desktop UI to show a
// concise reason banner on ApprovalGateCard; never weakens Go gate decisions
// and never surfaces secret values or raw file paths in `detail`.
import { maskCommandDataArguments } from "./command-data-args.js";
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
  hasMutatingHoxiScheduleCommand,
  isDestructivePermissionRequest,
  normalizeAbsoluteTool,
  shellWords,
  splitCommandChain,
  unwrapSandboxCommand,
} from "./permission-policy.js";
import { inlineScriptsReadOnly } from "./inline-script-readonly.js";
import { inlineScriptTool } from "./script-interpreter.js";
import { destructiveScanForms } from "./shell-substitution.js";

const SECRET_CMD_RE = new RegExp(
  [
    ENV_FILE_PATTERN,
    ENVRC_FILE_PATTERN,
    KEY_FILE_PATTERN,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:ssh|aws|gnupg|kube|azure|codex)(?:\/|$|[\s;&|)'"*?:,])`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?(?:\.config\/(?:opencode|gh|gcloud)|\.local\/share\/opencode)(?:\/|$|[\s;&|)'"*?])`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:[^\s;&|]*\/)?\.(?:netrc|npmrc|pypirc|git-credentials|credentials\.json|claude\.json)(?:$|[\s;&|)'"*?])`,
    String.raw`(?:^|[\s'"=(@<>:,])(?:~|\$HOME|\$\{HOME\})\/(?:\.cargo\/credentials|Library\/Keychains(?:\/|$)|\.hoxi(?:\/[*?]|\/?(?:$|[\s;&|)'"])))`,
    String.raw`(?:google\/chrome|chromium|bravesoftware|microsoft(?:\\?\s|_)?edge|vivaldi|opera|arc\/user(?:\\?\s|_)?data|firefox|mozilla|library\/cookies|library\/safari)[^;&|\n]*?[\/\\](?:cookies(?:\.sqlite|\.binarycookies)?|login(?:\\?\s|_)data|web(?:\\?\s|_)data|logins\.json|key4\.db)(?:$|[\s'";&|)*?-])`,
    String.raw`\$\{?XDG_CONFIG_HOME\}?\/(?:gh|gcloud|opencode)(?:\/|$|[\s;&|)'"*?])`,
    String.raw`\.claude\/\.credentials\.json`,
    String.raw`\.hoxi\/(?:models|media-roles|intake|collab|schedules|auth|tunnel(?:-dev)?|node-relay(?:-dev)?|cloudflared|pairing|relay-keys|work-apps|dataprofiles|approval-policy|hoxi)\.(?:json|db)`,
    String.raw`\.docker\/config\.json`,
    String.raw`\b(?:curl|wget|http)\b[^\n]*?(?:authorization|bearer|x-api-key|\$\{?[A-Za-z_][A-Za-z0-9_]*(?:_KEY|_TOKEN|_SECRET)\b)`,
  ].join("|"),
  "i"
);

const PROTECTED_WRITE_RE = new RegExp(
  [
    String.raw`[>|]\s*(?:\/etc\/|(?:\~|\$HOME|\$\{HOME\})\/\.(?:zshrc|zshenv|zprofile|bashrc|bash_profile|profile|ssh\b|gnupg\b|netrc\b|npmrc\b))`,
    String.raw`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*\/)?\.(?:git\/(?:hooks\/|config\b)|opencode\/|claude\/|agents\/|hoxi\/skills\/|vscode\/settings\.json\b|gemini\/(?:settings|config\/hooks)\.json\b)`,
    String.raw`(?:>>?|\b(?:cp|mv|install|ln|rsync|tee)\b[^\n;&|]*|\bsed\b[^\n;&|]*[ \t]-[a-z0-9]*i[^\n;&|]*)[ \t]*(?:[^\s;&|]*\/)?hoxi\.json(?:$|[\s;&|)'"*?])`,
    String.raw`(?:^|[;&|(\n])\s*cd\s+(?:-[LPe@]+\s+)*(?:\/(?:private\/)?etc\b|\/(?:private\/var\/|var\/)?root\b)`,
    String.raw`(?:^|[;&|(\n])\s*(?:\S*\/)?(?:ba|z|da)?sh\s+(?:-[^\s]+\s+)*["']?(?:[^\s;&|()<>'"]*\/)?(?:\.git\/|\.hoxi\/attachments\/|\.claude\/|\.opencode\/)`,
    String.raw`(?:^|[\s;&|(])(?:export\s+|env(?:\s+-\S+)*\s+)?BASH_ENV=`,
    String.raw`\b(?:ssh|scp)\b[^\n;&|]*\s-o\s*(?:proxycommand|localcommand|permitlocalcommand|knownhostscommand)\b`,
  ].join("|"),
  "i"
);

const PIPE_TO_SHELL_RE = /\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b/i;
const DOWNLOADER_RE = /(?:^|[\s;&|(])(?:\S*\/)?(?:curl|wget)\b/i;
const SHELL_SCRIPT_RUN_RE =
  /(?:^|[\s;&|(])(?:\S*\/)?(?:ba|z)?sh[ \t]+(?:-o[ \t]+\S+[ \t]+|-\S+[ \t]+)*["']?([^\s;&|()<>'"]+\.(?:sh|bash|zsh))/gi;
const BASH_ENV_RE = /(?:^|[\s;&|(])(?:export\s+|env(?:\s+-\S+)*\s+)?BASH_ENV=/;

const PUBLISH_OR_FORCE_RE = new RegExp(
  [
    String.raw`\bgit\b[^\n;&|]*?\b(?:branch\b[^\n;&|]*(?:\s--(?:delete|force)(?:[=\s]|$)|\s-[a-zA-Z]*[dDf][a-zA-Z]*(?:\s|$))|worktree\s+(?:remove|prune)\b|push\b[^\n;&|]*(?:\s--f(?:o(?:r\S*)?)?(?:[=\s]|$)|\s--d(?:e(?:l\S*)?)?(?:[=\s]|$)|\s--mi\S*|\s--pru\S*|\s-[a-zA-Z]*[fd][a-zA-Z]*(?:\s|$)|\s\+\S|\s:\S))`,
    String.raw`\b(?:npm|pnpm|yarn|bun|cargo)\s+(?:-r\s+|--recursive\s+)?publish\b`,
    String.raw`\bgh\s+(?:release\s+create|pr\s+merge)\b`,
    String.raw`\btwine\s+upload\b`,
    String.raw`\bgem\s+push\b`,
    String.raw`\bdocker\s+push\b`,
    String.raw`\bterraform\s+(?:destroy|apply)\b`,
    String.raw`\bkubectl\s+(?:apply|delete)\b`,
    String.raw`\bhelm\s+(?:install|upgrade)\b`,
    String.raw`(?:\.\/)?scripts\/ai-release\.sh\b`,
    String.raw`\bdocker\s+(?:system\s+prune|rm|rmi)\b`,
    String.raw`\/api\/hoxi\/(?:tools\/run|dataprofiles\b|intake\b|collab\b|schedules\b|computer\/(?:action|grant|pause|stop))\b`,
  ].join("|"),
  "i"
);

const PRIV_OR_PROCESS_RE =
  /(?:^|[\s;&|()<>\x60'"=:/{},$])(mkfs(?:\.[a-z0-9]+)?|(?:sudo|su|doas|chmod|chown|dd|diskutil|killall|reboot|shutdown)(?:\.(?:exe|cmd|bat))?)(?:$|[\s;&|()<>\x60'"\\{},$])/i;

const CREDENTIAL_ENV_NAMES = new Set([
  "SSH_AUTH_SOCK",
  "SSH_AGENT_PID",
  "GPG_AGENT_INFO",
  "GIT_ASKPASS",
  "SSH_ASKPASS",
  "SUDO_ASKPASS",
  "KUBECONFIG",
  "DOCKER_CONFIG",
  "NETRC",
  "AWS_SHARED_CREDENTIALS_FILE",
  "AWS_CONFIG_FILE",
  "GIT_CONFIG_PARAMETERS",
  "GIT_CONFIG_COUNT",
]);
const CREDENTIAL_ENV_FRAGMENTS = [
  "TOKEN",
  "SECRET",
  "PASSWORD",
  "PASSWD",
  "CREDENTIAL",
  "APIKEY",
  "API_KEY",
  "ACCESS_KEY",
  "PRIVATE_KEY",
];
const CREDENTIAL_ENV_PARTS = new Set(["AUTH", "PAT", "AUTHTOKEN"]);
const BRACE_ENV_OBFUSCATION_RE = /(?:^|[\s=])[^\s;&|'"]*\{[^\s;&|{}'"]*,[^\s;&|{}'"]*\}[^\s;&|'"]*/g;

function isCredentialEnvName(name) {
  const upper = String(name || "").trim().toUpperCase();
  if (!upper) return false;
  if (CREDENTIAL_ENV_NAMES.has(upper) || upper.startsWith("GIT_CONFIG_KEY_") || upper.startsWith("GIT_CONFIG_VALUE_")) {
    return true;
  }
  if (CREDENTIAL_ENV_FRAGMENTS.some((frag) => upper.includes(frag))) return true;
  if (upper.split("_").some((part) => CREDENTIAL_ENV_PARTS.has(part))) return true;
  return upper.endsWith("_KEY");
}

function referencesCredentialEnvVar(s) {
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < s.length) {
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === '"') {
        quote = "";
        continue;
      }
    } else if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c !== "$" || i + 1 >= s.length) continue;
    if (s[i + 1] === "{") {
      const m = /^\{[!#]?([A-Za-z_][A-Za-z0-9_]*)/.exec(s.slice(i + 1));
      if (m && isCredentialEnvName(m[1])) return true;
      continue;
    }
    const m = /^([A-Za-z_][A-Za-z0-9_]*)/.exec(s.slice(i + 1));
    if (m && isCredentialEnvName(m[1])) return true;
  }
  return false;
}

function hasBraceEnvSecret(s) {
  for (const m of s.matchAll(BRACE_ENV_OBFUSCATION_RE)) {
    const tok = m[0].trim();
    if (tok.startsWith("--exclude=") || tok.startsWith("--exclude-dir=")) continue;
    const open = tok.indexOf("{");
    const close = tok.indexOf("}", open + 1);
    if (open < 0 || close < 0) continue;
    const prefix = tok.slice(0, open);
    const suffix = tok.slice(close + 1);
    for (const alt of tok.slice(open + 1, close).split(",")) {
      const candidate = (prefix + alt + suffix).replace(/^-[^=]+=/, "");
      if (isSecretPath(candidate)) return true;
    }
  }
  return false;
}

function runsScriptNamedElsewhere(command) {
  for (const m of command.matchAll(SHELL_SCRIPT_RUN_RE)) {
    const base = m[1].replace(/\\/g, "/").split("/").pop();
    if (base && command.split(base).length - 1 > 1) return true;
  }
  return false;
}
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

function classifyBashRisk(candidates, fallbackHighRisk) {
  const rawForms = [];
  const forms = [];
  for (const cmd of candidates) {
    for (const f of destructiveScanForms(cmd)) {
      rawForms.push(f);
      forms.push(f);
      forms.push(f.replace(DEOBFUSCATE_RE, ""));
    }
  }
  const blob = forms.join("\n");
  if (SECRET_CMD_RE.test(blob) || rawForms.some(referencesCredentialEnvVar) || rawForms.some(hasBraceEnvSecret)) {
    return { code: "secret_read", severity: "danger" };
  }
  if (
    PROTECTED_WRITE_RE.test(blob) ||
    candidates.some((c) => BASH_ENV_RE.test(maskCommandDataArguments(unwrapSandboxCommand(c)))) ||
    candidates.some((c) => hasProtectedGitOutput(c, splitCommandChain, shellWords))
  ) {
    return { code: "protected_path", severity: "danger" };
  }
  if (PIPE_TO_SHELL_RE.test(blob) || rawForms.some((f) => DOWNLOADER_RE.test(f) && runsScriptNamedElsewhere(f))) {
    return { code: "pipe_to_shell", severity: "danger" };
  }
  if (hasMutatingHoxiScheduleCommand(blob)) {
    return { code: "external_tool", severity: "danger", detail: "hoxi_schedule" };
  }
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
  return fallbackHighRisk ? { code: "high_risk", severity: "danger" } : null;
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
    if (perm === "hoxi_schedule" || tool === "hoxi_schedule") {
      return { code: "external_tool", severity: "danger", detail: "hoxi_schedule" };
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
      return classifyBashRisk(candidates, true);
    }
    return { code: "high_risk", severity: "danger" };
  }

  if (isBash && candidates.length > 0) {
    const explicitRisk = classifyBashRisk(candidates, false);
    if (explicitRisk) return explicitRisk;
  }

  if (isBash && candidates.some(optsOutOfSandbox)) {
    return { code: "unsandboxed", severity: "warn", detail: "# hoxi:unsandboxed" };
  }

  if (isBash && isOpaquePermissionRequest(input, { worktree: options.worktree, readOnlyInlineScripts: true })) {
    for (const cmd of candidates) {
      const interp = detectInlineInterpreter(cmd);
      if (interp && !inlineScriptsReadOnly(unwrapSandboxCommand(cmd))) {
        return { code: "inline_script", severity: "warn", detail: interp };
      }
    }
    return { code: "opaque_shell", severity: "warn" };
  }

  const grantClasses = Array.isArray(meta.grantClasses) ? meta.grantClasses : [];
  for (const cls of grantClasses) {
    if (typeof cls !== "string") continue;
    if (cls.startsWith("ssh:") && cls.length > 4) {
      return { code: "remote_ssh", severity: "warn", detail: cls.slice(4) };
    }
    if (cls.startsWith("ssh-profile:") && cls.length > 12) {
      return { code: "remote_ssh", severity: "warn", detail: cls.slice(12) };
    }
  }

  return { code: "mode_policy", severity: "info" };
}
