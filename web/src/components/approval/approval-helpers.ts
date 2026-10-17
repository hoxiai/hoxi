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
