// Lexer-backed capability judge for inline Python and Node scripts in
// unattended mode and the on-demand sandbox. Mirrors
// internal/runner/shellpolicy/inline_script_capability.go.
import {
  claudeSkillPathHasAmbiguousInput,
  isProtectedShellReadPath,
  isSecretPath,
  isSensitiveExternalReadPath,
} from "./external-path-risk.js";
import {
  cleanInlinePosixPath,
  decodeInlineEscapes,
  lexInlineScript,
} from "./inline-script-literals.js";
import {
  collectInlinePathVars,
  matchingCloseParen,
  normalizeInlinePathAliases,
  rewriteRegexExecCalls,
} from "./inline-script-paths.js";
import { maskInlineDataFields } from "./inline-script-fields.js";

const STD_WRITE_RE = /\b(?:sys|process)\s*\.\s*(?:stdout|stderr)\s*\.\s*write\b/g;
const SAFE_PROCESS_RE = /\bprocess\s*\.\s*(?:argv|cwd|version|versions|platform|arch|pid|ppid|hrtime|memoryUsage|cpuUsage|uptime|stdin)\b/g;
const UNSAFE_PROCESS_RE = /\bprocess\b/;
const RE_COMPILE_RE = /\bre\s*\.\s*compile\s*\(/g;
const MAIN_GUARD_RE = /\b__(?:name|main|dirname|filename)__\b/g;
const SQL_EXEC_RE = /\.\s*execute\s*\(\s*/;
const READ_SQL_RE = /^\s*(?:select\b|pragma\s+(?:table_info|table_xinfo|index_list|index_info|index_xinfo|foreign_key_list|table_list|database_list)\b)/i;
const SQL_WRITE_RE = /\b(?:delete|drop|insert|update|alter|vacuum|attach|upsert|create|replace|reindex|pragma\s+\w+\s*=)\b/i;
const FIELD_RE = /^[A-Za-z_][A-Za-z0-9_.]*$/;
const LIT_KEY_FILE_RE = /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|\/)id_(?:rsa|dsa|ecdsa|ed25519)[a-z0-9_.-]*)$/i;
const CAPABILITY_BYPASS_RE = /\b(?:locals|setattr|delattr|vm|runin\w*context|constructor|createrequire|globalthis|reflect|proxy)\b|\bmodule\s*\.\s*_|\brequire\s*\([^)]*\)\s*\[|\]\s*\(/i;

const BLOCKED_NODE_MODULES = new Set([
  "child_process", "http", "https", "http2", "net", "tls", "dgram", "dns", "cluster",
  "worker_threads", "vm", "inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events", "sqlite",
]);

function maskPrintIdioms(codeMask) {
  return codeMask
    .replace(STD_WRITE_RE, "print")
    .replace(SAFE_PROCESS_RE, "proc_ro")
    .replace(RE_COMPILE_RE, "re.pattern(")
    .replace(MAIN_GUARD_RE, "MAIN");
}

function leadingPyStringLiteral(s) {
  let i = 0;
  let isF = false;
  while (i < s.length && i < 2 && "rRbBuUfF".includes(s[i])) {
    if (s[i] === "f" || s[i] === "F") isF = true;
    i++;
  }
  if (i >= s.length || (s[i] !== '"' && s[i] !== "'")) return null;
  let q = s[i];
  if (s.slice(i).startsWith(q + q + q)) q = q + q + q;
  const start = i + q.length;
  for (let j = start; j < s.length; j++) {
    if (s[j] === "\\") {
      j++;
      continue;
    }
    if (s.slice(j).startsWith(q)) {
      return { body: s.slice(start, j), n: j + q.length, isF };
    }
  }
  return null;
}

function plainFStringFields(body) {
  for (let i = 0; i < body.length; i++) {
    if (body[i] === "{") {
      if (body[i + 1] === "{") {
        i++;
        continue;
      }
      const end = body.indexOf("}", i);
      if (end < 0 || !FIELD_RE.test(body.slice(i + 1, end))) return false;
      i = end;
    } else if (body[i] === "}") {
      if (body[i + 1] === "}") {
        i++;
        continue;
      }
      return false;
    }
  }
  return true;
}

function maskReadOnlySQL(code) {
  let out = "";
  let rest = code;
  while (true) {
    const m = SQL_EXEC_RE.exec(rest);
    if (!m) return out + rest;
    const afterIdx = m.index + m[0].length;
    const after = rest.slice(afterIdx);
    const lit = leadingPyStringLiteral(after);
    const sql = lit ? decodeInlineEscapes(false, lit.body, false) : null;
    if (
      lit &&
      sql !== null &&
      (!lit.isF || plainFStringFields(sql)) &&
      READ_SQL_RE.test(sql) &&
      !SQL_WRITE_RE.test(sql) &&
      !sql.trim().replace(/;$/, "").includes(";")
    ) {
      out += rest.slice(0, m.index) + '.query("sql"';
      rest = after.slice(lit.n);
      continue;
    }
    out += rest.slice(0, afterIdx);
    rest = after;
  }
}

function validateInlineRequiresOnly(toks) {
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    if (t.kind === "tpl") {
      for (const sub of t.exprs) {
        if (!validateInlineRequiresOnly(sub)) return false;
      }
      continue;
    }
    if (t.kind !== "id") continue;
    const isProp = i > 0 && toks[i - 1].kind === "op" && toks[i - 1].val === ".";
    if (t.val === "from" && !isProp && i + 1 < toks.length && toks[i + 1].kind === "str") {
      const mod = toks[i + 1].val.replace(/^node:/, "");
      if (BLOCKED_NODE_MODULES.has(mod.split("/", 1)[0])) return false;
    }
    if (t.val === "require" && !isProp) {
      if (i + 1 >= toks.length || toks[i + 1].kind !== "op" || toks[i + 1].val !== "(") return false;
      const closeIdx = matchingCloseParen(toks, i + 1);
      if (closeIdx >= toks.length || closeIdx !== i + 3 || toks[i + 2].kind !== "str") return false;
      const mod = toks[i + 2].val.replace(/^node:/, "");
      if (BLOCKED_NODE_MODULES.has(mod.split("/", 1)[0])) return false;
    }
  }
  return true;
}

function isDisallowedInlineCapabilityLiteral(raw, secretRe) {
  if (claudeSkillPathHasAmbiguousInput(raw)) return true;
  const p = String(raw ?? "").trim();
  if (!p) return false;
  for (const cand of [p, cleanInlinePosixPath(p)]) {
    if (!cand) continue;
    if (
      isSecretPath(cand) ||
      isSensitiveExternalReadPath(cand) ||
      isProtectedShellReadPath(cand) ||
      LIT_KEY_FILE_RE.test(cand) ||
      secretRe.test(cand)
    ) {
      return true;
    }
  }
  return false;
}

export function unattendedInlineCodeCategory(family, code, scriptCapabilityRe, scanText, secretRe) {
  if (family !== "python" && family !== "python3" && family !== "node") {
    return scriptCapabilityRe.test(code) ? "dynamic" : scanText(code);
  }
  const isJS = family === "node";
  const rawLex = lexInlineScript(family, code);
  if (!rawLex) return "dynamic";
  if (isJS) rawLex.toks = normalizeInlinePathAliases(code, rawLex.toks);
  const maskedLex = lexInlineScript(family, isJS ? maskInlineDataFields(code, rawLex.toks) : code);
  if (!maskedLex) return "dynamic";
  const { regexVars } = collectInlinePathVars(family, code, rawLex.toks);
  const masked = maskPrintIdioms(rewriteRegexExecCalls(isJS, regexVars, maskedLex.mask));
  const scanned = scanText(masked);
  if (scanned) return scanned;
  if (scriptCapabilityRe.test(masked) || (isJS && UNSAFE_PROCESS_RE.test(masked)) || CAPABILITY_BYPASS_RE.test(masked)) {
    return "dynamic";
  }
  if (!validateInlineRequiresOnly(rawLex.toks)) return "dynamic";
  for (const lit of rawLex.lits) {
    if (isDisallowedInlineCapabilityLiteral(lit, secretRe)) return "secret";
  }
  return "";
}
