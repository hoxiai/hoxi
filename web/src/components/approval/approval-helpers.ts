// web/src/components/approval/approval-helpers.ts
import type { PermissionRequest, SnapshotFileDiff } from "../../services/sse-types";

export interface ExtractedPermissionDiff {
  file: string;
  patch?: string;
  diff?: SnapshotFileDiff;
  additions: number;
  deletions: number;
}

/**
 * Ported from kilocode permission-diff-utils.ts:
 * Extracts structured file diffs (`filediff`, `files[]`, or raw `diff`) from a PermissionRequest.
 */
export function extractPermissionDiffs(request: PermissionRequest): ExtractedPermissionDiff[] {
  const meta = request.metadata ?? {};

  // 1. Direct `filediff` object (patch or before/after)
  if (meta.filediff && typeof meta.filediff === "object") {
    const fd = meta.filediff as Record<string, unknown>;
    const file =
      typeof fd.file === "string"
        ? fd.file
        : typeof fd.filePath === "string"
          ? fd.filePath
          : typeof meta.filepath === "string"
            ? meta.filepath
            : "patch";
    const additions = typeof fd.additions === "number" ? fd.additions : 0;
    const deletions = typeof fd.deletions === "number" ? fd.deletions : 0;
    if (typeof fd.patch === "string" && fd.patch.trim()) {
      return [{ file, patch: fd.patch, additions, deletions }];
    }
    if (typeof fd.before === "string" || typeof fd.after === "string") {
      return [
        {
          file,
          diff: {
            file,
            before: typeof fd.before === "string" ? fd.before : "",
            after: typeof fd.after === "string" ? fd.after : "",
            additions,
            deletions,
          },
          additions,
          deletions,
        },
      ];
    }
  }

  // 2. Multi-file `files` array (apply_patch / multiedit)
  if (Array.isArray(meta.files)) {
    const list: ExtractedPermissionDiff[] = [];
    for (const raw of meta.files) {
      if (!raw || typeof raw !== "object") continue;
      const item = raw as Record<string, unknown>;
      const file =
        typeof item.relativePath === "string"
          ? item.relativePath
          : typeof item.filePath === "string"
            ? item.filePath
            : typeof item.file === "string"
              ? item.file
              : "";
      if (!file) continue;
      const additions = typeof item.additions === "number" ? item.additions : 0;
      const deletions = typeof item.deletions === "number" ? item.deletions : 0;
      if (typeof item.patch === "string" && item.patch.trim()) {
        list.push({ file, patch: item.patch, additions, deletions });
      } else if (typeof item.before === "string" || typeof item.after === "string") {
        list.push({
          file,
          diff: {
            file,
            before: typeof item.before === "string" ? item.before : "",
            after: typeof item.after === "string" ? item.after : "",
            additions,
            deletions,
          },
          additions,
          deletions,
        });
      }
    }
    if (list.length > 0) return list;
  }

  // 3. Raw `diff` string
  if (typeof meta.diff === "string" && meta.diff.trim()) {
    const file =
      typeof meta.filepath === "string"
        ? meta.filepath
        : typeof meta.filePath === "string"
          ? meta.filePath
          : request.patterns?.[0] || "patch";
    return [{ file, patch: meta.diff, additions: 0, deletions: 0 }];
  }

  return [];
}

const WILDCARD_RE = /[*?[]/;

/** A persisted allow rule must be exact; OpenCode matches saved patterns as globs. */
export function isExactRule(rule: string): boolean {
  return Boolean(rule.trim()) && !WILDCARD_RE.test(rule);
}

/**
 * Whether a rule may be written to the global permission config.
 *
 * Allows must be exact (`git status`, `src/main.ts`) — a saved glob would let
 * `git push --force` skip the approval gate. Denies may be broader, but never
 * the bare `*` catch-all: saving `edit: { "*": "deny" }` would disable the
 * tool for every session with no obvious way back.
 */
export function isPersistableRule(rule: string, decision: "allow" | "deny"): boolean {
  const trimmed = rule.trim();
  if (!trimmed) return false;
  if (decision === "allow") return isExactRule(trimmed);
  return trimmed !== "*";
}

/**
 * Exact allow rules Hoxi is willing to persist for a request.
 *
 * Wildcards are refused on purpose: OpenCode evaluates the persisted
 * `permission` patterns in its core before Hoxi sees the request, so saving
 * the suggested `git *` (or `npm *`) would let a destructive variant such as
 * `git push --force` run without ever reaching the approval gate.
 *
 * Only shell commands are persisted. Edit / webfetch patterns are paths and
 * URLs relative to one worktree, while `.hoxi/models.json` is global — saving
 * them would grant the same relative path in every project. Those stay
 * session-scoped through `reply: "always"`.
 */
export function exactAllowRules(
  request: PermissionRequest,
  destructive = false
): Record<string, "allow" | "deny"> {
  if (destructive) return {};
  const permission = String(request.permission || "bash").toLowerCase();
  if (permission !== "bash" && permission !== "shell") return {};
  const rules: Record<string, "allow" | "deny"> = {};
  for (const raw of request.patterns ?? []) {
    const resource = String(raw ?? "").trim();
    if (!isExactRule(resource)) continue;
    rules[resource] = "allow";
  }
  return rules;
}

/**
 * Rules persisted alongside a reply. `always` saves the exact allow rules so
 * the decision survives a restart instead of only this session; `reject` only
 * keeps explicitly picked deny rules. Destructive requests persist nothing.
 */
export function rulesForReply(
  request: PermissionRequest,
  mode: "once" | "always" | "reject",
  destructive: boolean,
  selected: Record<string, "allow" | "deny">,
  override?: Record<string, "allow" | "deny">
): Record<string, "allow" | "deny"> {
  if (override) return override;
  if (destructive) return {};
  const denied = persistable(selected, "deny");
  if (mode === "reject") return denied;
  if (mode === "always") {
    const allowed = { ...exactAllowRules(request), ...persistable(selected, "allow") };
    for (const rule of Object.keys(denied)) delete allowed[rule];
    return allowed;
  }
  return {};
}

function persistable(
  selected: Record<string, "allow" | "deny">,
  decision: "allow" | "deny"
): Record<string, "allow" | "deny"> {
  return Object.fromEntries(
    Object.entries(selected).filter(
      ([rule, picked]) => picked === decision && isPersistableRule(rule, picked)
    )
  );
}

/**
 * Derives hierarchical command/file rules from `request.always`, `metadata.rules`, or bash tokens.
 */
export function deriveHierarchicalRules(request: PermissionRequest, commandStr: string): string[] {
  const metaRules = Array.isArray(request.metadata?.rules)
    ? (request.metadata.rules as string[])
    : [];
  const alwaysRules = Array.isArray(request.always) ? request.always : [];
  const combined = [...new Set([...metaRules, ...alwaysRules].filter(Boolean))];
  if (combined.length > 0) return combined;

  if (commandStr) {
    const tokens = commandStr.trim().split(/\s+/);
    const derived: string[] = [];
    if (tokens.length >= 2) derived.push(`${tokens[0]} ${tokens[1]} *`);
    if (tokens.length >= 1 && tokens[0]) derived.push(`${tokens[0]} *`);
    return derived;
  }
  return [];
}
