// Mirrors unattendedCommandCategory and unattendedDynamicCategory in
// internal/runner/approval_unattended.go and approval_unattended_dynamic.go.
// The shared fixture .opencode/lib/unattended.cases.json keeps both aligned.
// A deny is final: no approval mode and no session grant relaxes it.
//
// The static classes (publish, privilege, egress, secret) are best effort: a
// script file the session wrote runs as written. Code built at run time
// (eval, a pipe into a shell, `$CMD args`, inline interpreter code that can
// run processes) is refused as "dynamic" because those classes cannot see
// through it. The OS sandbox and the scrubbed runtime environment are what
// hold when the static rules miss (AGENTS.md rule 6).

import { parseUnattendedScript, INDIRECT_EXPANSION } from "./unattended-shell.js";
import { inlineScriptTool } from "./script-interpreter.js";
import { destructiveScanForms } from "./shell-substitution.js";
import { isOpaqueShellCommand } from "./opaque-command.js";
import { unwrapSandboxCommand } from "./permission-policy.js";

const publishRe = /(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:git(?:\s+-C\s+\S+|\s+-c\s+\S+|\s+--git-dir=\S+|\s+--work-tree=\S+)*\s+push|gh\s+(?:release\s+create|pr\s+merge)|(?:npm|pnpm|yarn|bun)\s+(?:-r\s+|--recursive\s+)?publish|cargo\s+publish|twine\s+upload|gem\s+push|docker\s+push|terraform\s+(?:apply|destroy)|kubectl\s+(?:apply|delete)|helm\s+(?:install|upgrade)|(?:\.\/)?scripts\/ai-release\.sh)\b/i;
const sudoRe = /(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:sudo|su|doas)\b/i;
const egressRe = /\b(?:curl|wget|http)\b[^\n]*?(?:authorization|bearer|x-api-key|\$\{?[A-Za-z_][A-Za-z0-9_]*(?:_KEY|_TOKEN|_SECRET)\b)/i;
const secretRe = /(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/(?:\.ssh|\.aws|\.gnupg|\.kube|\.azure)(?:\/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.docker\/config\.json|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.config\/(?:gh|gcloud)(?:\/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.(?:netrc|npmrc|pypirc|git-credentials)\b|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.cargo\/credentials|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/Library\/Keychains(?:\/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.hoxi\/(?:relay-keys|tunnel(?:-dev)?|auth|cloudflared|pairing|approval-policy|models|hoxi)\.(?:json|db)\b|(?:^|[\s'"=(<>:,])\.env(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?))?(?:$|[\s;&|'"*?])/i;
const wrappedRe = /(?:bash|sh|zsh|eval|xargs)\b[^|&;\n]*\b(?:git\s+push|sudo|publish)\b/i;

// Kept byte-identical with unattendedScriptCapabilityRe in Go.
const SCRIPT_CAPABILITY_RE = /\b(?:subprocess|system|popen\w*|spawn\w*|exec\w*|fork\w*|pty|commands|shell_exec|passthru|proc_open|open3|ctypes|cffi|dlopen|eval|compile|importlib|getattr|vars|globals|pickle|marshal|environ|getenv|putenv|ENV|socket|urllib\d?|requests|httpx|aiohttp|http|https|smtplib|ftplib|telnetlib|paramiko|net|tls|dgram|undici|fetch|XMLHttpRequest|WebSocket|LWP|URI|ObjC|NSTask|Function)\b|__\w+__|child_process|worker_threads|process\s*\.\s*(?:env|binding|dlopen|mainModule)|sys\s*\.\s*modules|\brequire\s*\(\s*[^'"\s)]|\bimport\s*\(|Deno\s*\.\s*(?:run|Command|env)|Bun\s*\.\s*(?:spawn|env|\$)|Net::|HTTP::|IO::Socket|%x|\bqx\b|[Dd]o\s+[Ss]hell\s+[Ss]cript|`/;
const GIT_EXEC_CONFIG_RE = /^(?:alias\.|core\.(?:pager|editor|sshcommand|fsmonitor|hookspath|askpass)|sequence\.editor|diff\.external|credential\.|gpg\.|uploadpack\.|receivepack\.|[^=]*\.(?:command|cmd|textconv|process|program|helper|clean|smudge)=)/i;
const ASSIGN_RE = /^[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?\+?=/;
const MAX_DEPTH = 4;

const set = (...items) => new Set(items);
const SHELLS = set("sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish", "csh", "tcsh");
const EXTRA_INTERPRETERS = set("osascript", "deno", "bun", "lua", "rscript", "pwsh", "powershell", "tclsh", "irb");
const SENSITIVE_TOOLS = set("git", "gh", "npm", "pnpm", "yarn", "bun", "cargo", "twine", "gem", "docker",
  "terraform", "kubectl", "helm", "security", "aws", "gcloud", "az");
const NETWORK_TOOLS = set("curl", "wget", "http", "https", "xh", "httpie", "nc", "ncat", "netcat", "socat",
  "telnet", "ssh", "scp", "sftp", "rsync", "ftp", "tftp", "openssl", "gh", "git", "aws", "gcloud", "az",
  "dig", "nslookup", "host", "ping", "mail", "sendmail");
const ENV_DUMPERS = set("env", "printenv", "set", "export", "declare", "typeset", "compgen");
const KEYWORDS = set("{", "}", "!", "if", "then", "else", "elif", "fi", "do", "done", "while", "until", "esac", "coproc", "time", "noglob", "nocorrect");
const GIT_GLOBAL_VALUE_FLAGS = set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path");
const INTERP_VALUE_FLAGS = set("-m", "-W", "-X", "-r", "--require", "--import", "-I", "-C");
const KNOWN_GIT_SUBCOMMANDS = set("add", "am", "annotate", "apply", "archive", "bisect", "blame", "branch", "bundle",
  "cat-file", "check-attr", "check-ignore", "check-mailmap", "check-ref-format", "checkout", "checkout-index",
  "cherry", "cherry-pick", "clean", "clone", "column", "commit", "commit-graph", "commit-tree", "config",
  "count-objects", "describe", "diff", "diff-files", "diff-index", "diff-tree", "difftool", "fetch",
  "fetch-pack", "for-each-ref", "format-patch", "fsck", "gc", "grep", "hash-object", "help", "init",
  "interpret-trailers", "lfs", "log", "ls-files", "ls-remote", "ls-tree", "maintenance", "merge", "merge-base",
  "merge-file", "merge-tree", "mergetool", "mktree", "mv", "name-rev", "notes", "pack-refs", "patch-id",
  "prune", "pull", "push", "range-diff", "read-tree", "rebase", "reflog", "remote", "repack", "replace",
  "request-pull", "rerere", "reset", "restore", "rev-list", "rev-parse", "revert", "rm", "send-email",
  "send-pack", "http-push", "shortlog", "show", "show-branch", "show-ref", "sparse-checkout", "stash", "status",
  "stripspace", "submodule", "subtree", "switch", "symbolic-ref", "tag", "update-index", "update-ref", "var",
  "verify-commit", "verify-tag", "version", "whatchanged", "worktree", "write-tree", "credential",
  "credential-osxkeychain", "credential-store", "credential-cache", "fast-export", "fast-import",
  "filter-branch", "gui", "citool", "hook", "index-pack", "pack-objects", "unpack-objects", "verify-pack",
  "mktag", "multi-pack-index", "prune-packed", "update-server-info", "fmt-merge-msg", "bugreport",
  "show-index", "get-tar-commit-id", "mailinfo", "mailsplit", "merge-index", "replay", "unpack-file");

// Mirrors isCredentialEnvName in internal/runner/sandbox_env.go.
const CREDENTIAL_NAMES = set("SSH_AUTH_SOCK", "SSH_AGENT_PID", "GPG_AGENT_INFO", "GIT_ASKPASS", "SSH_ASKPASS",
  "SUDO_ASKPASS", "KUBECONFIG", "DOCKER_CONFIG", "NETRC", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE",
  "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT");
const CREDENTIAL_FRAGMENTS = ["TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "APIKEY", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY"];
const CREDENTIAL_PARTS = set("AUTH", "PAT", "AUTHTOKEN");

export function isCredentialEnvName(name) {
  const upper = String(name ?? "").trim().toUpperCase();
  if (!upper) return false;
  if (CREDENTIAL_NAMES.has(upper) || upper.startsWith("GIT_CONFIG_KEY_") || upper.startsWith("GIT_CONFIG_VALUE_")) return true;
  if (CREDENTIAL_FRAGMENTS.some((f) => upper.includes(f))) return true;
  if (upper.split("_").some((p) => CREDENTIAL_PARTS.has(p))) return true;
  return upper.endsWith("_KEY");
}

export function unattendedCommandDecision(command) {
  const cat = unattendedCommandCategory(command) || unattendedDynamicCategory(unwrapSandboxCommand(String(command ?? "")));
  return cat ? { expect: "deny", category: cat } : { expect: "allow", category: "" };
}

// Mirrors unattendedCommandCategory: data arguments are masked so a commit
// message that mentions "git push" is not a push, `<(…)`/`$(…)` bodies are
// unpacked, and an opaque command is scanned again as raw text.
function unattendedCommandCategory(command) {
  const cmd = unwrapSandboxCommand(String(command ?? ""));
  for (const form of destructiveScanForms(cmd)) {
    const cat = scanUnattendedText(form);
    if (cat) return cat;
  }
  return isOpaqueShellCommand(cmd, "") ? scanUnattendedText(cmd) : "";
}

function scanUnattendedText(text) {
  if (publishRe.test(text) || wrappedRe.test(text)) return "publish";
  if (sudoRe.test(text)) return "privilege";
  if (egressRe.test(text)) return "egress";
  if (secretRe.test(text)) return "secret";
  return "";
}

export function unattendedDynamicCategory(command) {
  const ctx = { credRef: false, envDump: false, network: false };
  const cat = scriptCategory(String(command ?? ""), 0, ctx);
  if (cat) return cat;
  return (ctx.credRef || ctx.envDump) && ctx.network ? "egress" : "";
}

function scriptCategory(text, depth, ctx) {
  if (depth > MAX_DEPTH) return "dynamic";
  if (depth > 0) {
    const cat = scanUnattendedText(text);
    if (cat) return cat;
  }
  const sc = parseUnattendedScript(text);
  return sc ? treeCategory(sc, depth, ctx) : "dynamic";
}

function treeCategory(sc, depth, ctx) {
  for (const name of sc.vars) if (name === INDIRECT_EXPANSION || isCredentialEnvName(name)) ctx.credRef = true;
  if (!sc.dataOnly) {
    for (const seg of sc.segs) {
      const cat = segCategory(seg, depth, ctx);
      if (cat) return cat;
    }
  }
  for (const child of sc.children) {
    const cat = treeCategory(child, depth + 1, ctx);
    if (cat) return cat;
  }
  return "";
}

const baseName = (p) => p.slice(p.lastIndexOf("/") + 1).toLowerCase();

function segCategory(seg, depth, ctx) {
  let words = seg.words;
  while (words.length > 0 && !words[0].dynamic) {
    const t = words[0].text;
    if (t === "function" && words.length > 1) {
      words = words.slice(2);
      continue;
    }
    if (!KEYWORDS.has(t)) break;
    words = words.slice(1);
  }
  if (words.length > 0 && !words[0].dynamic && ["case", "for", "select", "in"].includes(words[0].text)) return "";
  while (words.length > 0 && ASSIGN_RE.test(words[0].text)) words = words.slice(1);
  const [cmd, cat] = unwrapWrappers(words);
  if (cat || cmd.length === 0) return cat;
  return argvWordsCategory(seg, cmd, depth, ctx);
}

function argvWordsCategory(seg, words, depth, ctx) {
  const head = words[0];
  if (head.dynamic || head.procSub) return "dynamic";
  const base = baseName(head.text);
  const argv = words.map((w) => w.text);
  const cat = argvCategory(argv);
  if (cat) return cat;
  if (NETWORK_TOOLS.has(base)) ctx.network = true;
  if (ENV_DUMPERS.has(base) && dumpsEnv(base, argv.slice(1), ctx)) ctx.envDump = true;
  const args = words.slice(1);
  if (base === "eval") return "dynamic";
  if (base === "alias" && args.length > 0) return "dynamic";
  if (base === "source" || base === ".") {
    return args.length > 0 && (args[0].dynamic || args[0].procSub || isStdinPath(args[0].text)) ? "dynamic" : "";
  }
  if (base === "parallel") return "dynamic";
  if (SHELLS.has(base)) return shellCategory(seg, args, depth, ctx);
  if (base === "xargs") return xargsCategory(args, depth, ctx);
  if (base === "find") return findCategory(args, depth, ctx);
  if (base === "watch") return watchCategory(args, depth, ctx);
  const family = interpreterFamily(base);
  if (family) return interpreterCategory(seg, family, args);
  if (SENSITIVE_TOOLS.has(base)) return toolCategory(base, args, depth, ctx);
  return "";
}

// Mirrors unattendedArgvCategory in approval_unattended.go.
function argvCategory(words) {
  const base = baseName(words[0]);
  const rest = words.slice(1);
  switch (base) {
    case "sudo": case "su": case "doas": return "privilege";
    case "git": if (gitHasSubcommand(rest, "push")) return "publish"; break;
    case "gh":
      if (rest[0] === "release" && rest[1] === "create") return "publish";
      if (rest[0] === "pr" && rest[1] === "merge") return "publish";
      break;
    case "npm": case "pnpm": case "yarn": case "bun": case "cargo": if (argvHas(rest, "publish")) return "publish"; break;
    case "twine": if (argvHas(rest, "upload")) return "publish"; break;
    case "gem": case "docker": if (argvHas(rest, "push")) return "publish"; break;
    case "terraform": if (argvHas(rest, "apply") || argvHas(rest, "destroy")) return "publish"; break;
    case "kubectl": if (argvHas(rest, "apply") || argvHas(rest, "delete")) return "publish"; break;
    case "helm": if (argvHas(rest, "install") || argvHas(rest, "upgrade")) return "publish"; break;
    case "curl": case "wget": case "http": if (egressRe.test(words.join(" "))) return "egress"; break;
  }
  if (base === "ai-release.sh" || words[0].toLowerCase().endsWith("scripts/ai-release.sh")) return "publish";
  return "";
}

function gitHasSubcommand(args, name) {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--") return false;
    if (["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"].includes(a)) i++;
    else if (!a.startsWith("-")) return a === name;
  }
  return false;
}

function argvHas(args, name) {
  for (const a of args) {
    if (a === "--") return false;
    if (!a.startsWith("-") && a === name) return true;
  }
  return false;
}

function skipOptions(args, valueFlags, positional) {
  let i = 0;
  while (i < args.length && args[i].text.startsWith("-") && args[i].text !== "-") {
    if (args[i].text === "--") {
      i++;
      break;
    }
    if (valueFlags.has(args[i].text)) i++;
    i++;
  }
  while (positional-- > 0 && i < args.length) i++;
  return args.slice(Math.min(i, args.length));
}

// Mirrors unwrapUnattendedWrappers: returns [words, category].
function unwrapWrappers(words) {
  while (words.length > 0) {
    if (words[0].dynamic) return [words, ""];
    const args = words.slice(1);
    switch (baseName(words[0].text)) {
      case "nohup": case "builtin": case "noglob": case "nocorrect": case "unbuffer": case "chronic": case "setsid":
        words = args; break;
      case "time": words = skipOptions(args, set(), 0); break;
      case "exec": words = skipOptions(args, set("-a"), 0); break;
      case "command":
        if (args.some((a) => a.text === "-v" || a.text === "-V")) return [[], ""];
        words = skipOptions(args, set(), 0);
        break;
      case "env": {
        let i = 0;
        for (; i < args.length; i++) {
          const t = args[i].text;
          if (args[i].dynamic) return [[], "dynamic"];
          if (t === "-S" || t.startsWith("--split-string") || (t.startsWith("-") && !t.startsWith("--") && t.includes("S"))) return [[], "dynamic"];
          if (["-u", "-P", "-C", "--unset", "--chdir"].includes(t)) {
            i++;
            continue;
          }
          if (t === "--") {
            i++;
            break;
          }
          if (t.startsWith("-") || ASSIGN_RE.test(t)) continue;
          break;
        }
        if (i >= args.length) return [[words[0]], ""];
        words = args.slice(i);
        break;
      }
      case "nice": words = skipOptions(args, set("-n", "--adjustment"), 0); break;
      case "timeout": case "gtimeout": words = skipOptions(args, set("-s", "--signal", "-k", "--kill-after"), 1); break;
      case "stdbuf": words = skipOptions(args, set("-i", "-o", "-e"), 0); break;
      case "caffeinate": words = skipOptions(args, set("-t", "-w"), 0); break;
      case "arch": words = skipOptions(args, set("-arch", "-e"), 0); break;
      case "xcrun":
        if (args.some((a) => a.text === "-f" || a.text === "--find")) return [[], ""];
        words = skipOptions(args, set("-sdk", "--sdk", "--toolchain", "-toolchain"), 0);
        break;
      case "flock": words = skipOptions(args, set("-w", "--timeout", "-E", "--conflict-exit-code"), 1); break;
      default: return [words, ""];
    }
  }
  return [words, ""];
}

function dumpsEnv(base, args, ctx) {
  switch (base) {
    case "printenv":
      if (args.some(isCredentialEnvName)) ctx.credRef = true;
      return args.length === 0;
    case "env": case "set": return args.length === 0;
    case "export": case "declare": case "typeset": return args.length === 0 || args[0] === "-p" || args[0] === "-x";
    case "compgen": return args.length > 0 && (args[0] === "-v" || args[0] === "-e");
  }
  return false;
}

function isStdinPath(p) {
  return p === "/dev/stdin" || p === "/dev/fd/0" || p === "/proc/self/fd/0";
}

function shellCategory(seg, args, depth, ctx) {
  let cmdMode = false;
  let stdinMode = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a.dynamic || a.procSub) return "dynamic";
    const t = a.text;
    if (t === "-") {
      stdinMode = true;
      break;
    }
    if (t === "--") {
      if (i + 1 < args.length && !stdinMode) return shellScriptArg(args[i + 1], cmdMode, depth, ctx);
      break;
    }
    if (t.length > 1 && (t[0] === "-" || t[0] === "+")) {
      if (["-o", "+o", "-O", "+O", "--rcfile", "--init-file"].includes(t)) {
        i++;
        continue;
      }
      if (!t.startsWith("--")) {
        cmdMode = cmdMode || t.slice(1).includes("c");
        stdinMode = stdinMode || t.slice(1).includes("s");
      }
      continue;
    }
    if (stdinMode) break;
    return shellScriptArg(a, cmdMode, depth, ctx);
  }
  if (cmdMode && !stdinMode) return "";
  return stdinScriptCategory(seg, (text) => scriptCategory(text, depth + 1, ctx));
}

function shellScriptArg(a, cmdMode, depth, ctx) {
  if (a.dynamic || a.procSub) return "dynamic";
  if (cmdMode) return scriptCategory(a.text, depth + 1, ctx);
  return isStdinPath(a.text) ? "dynamic" : "";
}

function stdinScriptCategory(seg, judge) {
  if (seg.stdin === "procsub") return "dynamic";
  if (seg.stdin === "heredoc" || seg.stdin === "herestring") return seg.stdinDynamic ? "dynamic" : judge(seg.stdinText);
  if (seg.stdin === "file") return "";
  return seg.pipedIn ? "dynamic" : "";
}

function interpreterFamily(base) {
  const family = inlineScriptTool(base);
  if (family) return family;
  return EXTRA_INTERPRETERS.has(base) ? base : "";
}

function inlineFlag(family, arg) {
  switch (family) {
    case "python": case "python3": return arg === "-c";
    case "node": case "bun": return ["-e", "--eval", "-p", "--print"].includes(arg);
    case "deno": return arg === "eval";
    case "perl": case "ruby": return arg.startsWith("-") && !arg.startsWith("--") && /[eE]/.test(arg.slice(1));
    case "php": return arg === "-r";
    case "pwsh": case "powershell": {
      const lower = arg.toLowerCase();
      return lower === "-c" || lower === "-command" || lower.startsWith("-e");
    }
    default: return arg === "-e";
  }
}

function interpreterCategory(seg, family, args) {
  const judge = (code) => (SCRIPT_CAPABILITY_RE.test(code) ? "dynamic" : scanUnattendedText(code));
  const options = [];
  let hasCode = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a.dynamic || a.procSub) return "dynamic";
    const t = a.text;
    if (inlineFlag(family, t)) {
      if (i + 1 >= args.length) return "";
      const code = args[i + 1];
      if (code.dynamic || code.procSub) return "dynamic";
      if ((family === "pwsh" || family === "powershell") && t.toLowerCase().startsWith("-e")) return "dynamic";
      const cat = judge(code.text);
      if (cat) return cat;
      hasCode = true;
      i++;
      continue;
    }
    if (t === "-" || isStdinPath(t)) break;
    if (t.startsWith("-")) {
      options.push(t);
      if (INTERP_VALUE_FLAGS.has(t)) {
        if (i + 1 < args.length) options.push(args[i + 1].text);
        i++;
      }
      continue;
    }
    if (hasCode) break;
    return "";
  }
  if (hasCode) return judge(options.join(" "));
  return stdinScriptCategory(seg, judge);
}

function xargsCategory(args, depth, ctx) {
  const valueFlags = set("-I", "-J", "-L", "-n", "-P", "-R", "-S", "-s", "-E", "-d", "-a", "--max-args",
    "--max-procs", "--delimiter", "--arg-file", "--replace", "--eof", "--max-lines", "--max-chars");
  let replace = "";
  let i = 0;
  while (i < args.length && args[i].text.startsWith("-")) {
    const t = args[i].text;
    if (args[i].dynamic) return "dynamic";
    if (t === "--") {
      i++;
      break;
    }
    if (t === "-I" || t === "-J" || t === "--replace") {
      if (i + 1 < args.length) replace = args[i + 1].text;
    } else if (t.startsWith("-I") || t.startsWith("-J")) replace = t.slice(2);
    else if (t.startsWith("--replace=")) replace = t.slice("--replace=".length);
    if (valueFlags.has(t)) i++;
    i++;
  }
  if (i >= args.length) return "";
  return runnerCategory(args.slice(i), replace, true, depth, ctx);
}

function findCategory(args, depth, ctx) {
  for (let i = 0; i < args.length; i++) {
    if (!["-exec", "-execdir", "-ok", "-okdir"].includes(args[i].text)) continue;
    let j = i + 1;
    while (j < args.length && args[j].text !== ";" && args[j].text !== "+") j++;
    if (j > i + 1) {
      const cat = runnerCategory(args.slice(i + 1, j), "{}", false, depth, ctx);
      if (cat) return cat;
    }
    i = j;
  }
  return "";
}

function watchCategory(args, depth, ctx) {
  let i = 0;
  while (i < args.length && args[i].text.startsWith("-")) {
    if (args[i].text === "-n" || args[i].text === "--interval") i++;
    i++;
  }
  if (i >= args.length) return "";
  const rest = args.slice(i);
  if (rest.some((a) => a.dynamic || a.procSub)) return "dynamic";
  return scriptCategory(rest.map((a) => a.text).join(" "), depth + 1, ctx);
}

function runnerCategory(argv, placeholder, appendInput, depth, ctx) {
  let substituted = false;
  const words = argv.map((w) => {
    if (placeholder !== "" && w.text.includes(placeholder)) {
      substituted = true;
      return { ...w, dynamic: true };
    }
    return w;
  });
  if (appendInput && !substituted) words.push({ text: "", dynamic: true, procSub: false });
  const [cmd, cat] = unwrapWrappers(words);
  if (cat || cmd.length === 0) return cat;
  return argvWordsCategory({ words: [], pipedIn: false, stdin: "", stdinText: "", stdinDynamic: false }, cmd, depth + 1, ctx);
}

function toolCategory(base, args, depth, ctx) {
  if (base === "git") return gitCategory(args, depth, ctx);
  let idx = -1;
  for (let i = 0; i < args.length; i++) {
    if (args[i].dynamic && !args[i].text.startsWith("-")) return "dynamic";
    if (!args[i].text.startsWith("-")) {
      idx = i;
      break;
    }
  }
  if (idx < 0) return "";
  const sub = args[idx].text;
  const rest = args.slice(idx + 1);
  const restText = rest.map((a) => a.text);
  const next = restText[0] ?? "";
  switch (base) {
    case "npm": case "pnpm": case "yarn": case "bun":
      if (sub.length >= 3 && "publish".startsWith(sub)) return "publish";
      break;
    case "gh":
      if (rest.length > 0 && rest[0].dynamic) return "dynamic";
      if (sub === "auth" && (next === "token" || (next === "status" && (restText.includes("-t") || restText.includes("--show-token"))))) return "secret";
      if (sub === "api" && ghApiWrites(restText)) return "publish";
      if ((sub === "release" && ["upload", "edit", "delete"].includes(next)) || (sub === "gist" && next === "create") ||
        (sub === "repo" && ["create", "delete", "edit"].includes(next))) return "publish";
      break;
    case "security":
      if (["find-generic-password", "find-internet-password", "dump-keychain", "export", "find-key", "export-keychain"].includes(sub)) return "secret";
      break;
  }
  return "";
}

function ghApiWrites(args) {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "-X" || a === "--method") {
      if (i + 1 < args.length && args[i + 1].toUpperCase() !== "GET") return true;
    } else if ((a.startsWith("--method=") && a.slice(9).toUpperCase() !== "GET") ||
      (a.startsWith("-X") && a.length > 2 && a.slice(2).toUpperCase() !== "GET")) return true;
    else if (["-f", "-F", "--field", "--raw-field", "--input"].includes(a) ||
      a.startsWith("--field=") || a.startsWith("--raw-field=") || a.startsWith("--input=")) return true;
  }
  return false;
}

function gitCategory(args, depth, ctx) {
  let i = 0;
  while (i < args.length && args[i].text.startsWith("-")) {
    const t = args[i].text;
    if (args[i].dynamic) return "dynamic";
    if (t === "-c" || t === "--config-env") {
      const v = args[i + 1];
      if (v && (v.dynamic || GIT_EXEC_CONFIG_RE.test(v.text))) return "dynamic";
      i += 2;
      continue;
    }
    if (GIT_GLOBAL_VALUE_FLAGS.has(t)) i++;
    i++;
  }
  if (i >= args.length) return "";
  const sub = args[i];
  if (sub.dynamic) return "dynamic";
  const rest = args.slice(i + 1);
  const restText = rest.map((a) => a.text);
  if (sub.text === "send-email") return "publish";
  if (sub.text === "credential" || sub.text.startsWith("credential-")) return "secret";
  if (sub.text === "lfs" || sub.text === "subtree") return argvHas(restText, "push") ? "publish" : "";
  if (sub.text === "submodule") {
    const j = restText.indexOf("foreach");
    return j >= 0 ? joinedCategory(rest.slice(j + 1), depth, ctx, true) : "";
  }
  if (sub.text === "rebase") {
    for (let j = 0; j < rest.length; j++) {
      const a = rest[j];
      if ((a.text === "-x" || a.text === "--exec") && j + 1 < rest.length) {
        const cat = joinedCategory(rest.slice(j + 1, j + 2), depth, ctx, false);
        if (cat) return cat;
      } else if (a.text.startsWith("--exec=")) {
        if (a.dynamic) return "dynamic";
        const cat = scriptCategory(a.text.slice("--exec=".length), depth + 1, ctx);
        if (cat) return cat;
      }
    }
    return "";
  }
  if (sub.text === "bisect") {
    return restText[0] === "run" && rest.length > 1 ? runnerCategory(rest.slice(1), "", false, depth, ctx) : "";
  }
  return KNOWN_GIT_SUBCOMMANDS.has(sub.text) ? "" : "dynamic";
}

function joinedCategory(words, depth, ctx, skipOptions) {
  const parts = [];
  for (const w of words) {
    if (skipOptions && parts.length === 0 && w.text.startsWith("--")) continue;
    if (w.dynamic || w.procSub) return "dynamic";
    parts.push(w.text);
  }
  return parts.length === 0 ? "" : scriptCategory(parts.join(" "), depth + 1, ctx);
}
