// web/src/components/timeline/tool-card-state.ts
import { workspace } from "../../store/workspace";
import {
  countDiffLines,
  type DiagnosticItem,
  type ToolPart,
} from "./tool-card-helpers";

export function resolveLoadedContextFiles(part: ToolPart): string[] {
  const state = part.state;
  if (state.status !== "completed") return [];
  const loaded = state.metadata?.loaded;
  if (!Array.isArray(loaded)) return [];
  const root = workspace.rootDir;
  return loaded
    .filter((entry): entry is string => typeof entry === "string")
    .map((entry) =>
      root && entry.startsWith(root) ? entry.slice(root.length).replace(/^\/+/, "") : entry
    );
}

export function resolveToolArgPills(part: ToolPart): string[] {
  const input = part.state.input || {};
  const pills: string[] = [];
  if (typeof input.offset === "number") pills.push(`offset=${input.offset}`);
  if (typeof input.limit === "number") pills.push(`limit=${input.limit}`);
  if (typeof input.pattern === "string" && input.pattern) pills.push(`pattern=${input.pattern}`);
  if (typeof input.include === "string" && input.include) pills.push(`include=${input.include}`);
  if (part.state.status === "completed") {
    const matches = part.state.metadata?.matches ?? part.state.metadata?.count;
    if (typeof matches === "number") {
      pills.push(`${matches} ${matches === 1 ? "match" : "matches"}`);
    }
  }
  return pills;
}

export function resolveToolTodoItems(part: ToolPart): Array<{ content: string; status: string }> {
  const state = part.state;
  const raw =
    (state.status !== "pending" ? state.metadata?.todos : undefined) ?? state.input?.todos;
  if (!Array.isArray(raw)) return [];
  return raw.filter((item) => item && typeof item.content === "string");
}

export function resolveToolFileDiff(
  part: ToolPart,
  isEdit: boolean,
  file: string
): { file: string; patch?: string; additions: number; deletions: number } | undefined {
  if (!isEdit || !file) return undefined;
  const diffs = workspace.diffsBySession[part.sessionID] ?? [];
  const sessionDiff = diffs.find(
    (d) => d.file === file || d.file.endsWith(`/${file}`) || file.endsWith(`/${d.file}`)
  );
  if (sessionDiff) return sessionDiff;

  const state = part.state;
  const meta = state.status !== "pending" ? state.metadata : undefined;
  const patch = meta?.diff || meta?.patch;
  const before = meta?.filediff?.before ?? state.input?.oldString;
  const after = meta?.filediff?.after ?? state.input?.newString ?? state.input?.content;
  if (patch || typeof after === "string") {
    const counts = countDiffLines(
      typeof patch === "string" ? patch : undefined,
      typeof before === "string" ? before : undefined,
      typeof after === "string" ? after : undefined
    );
    return {
      file,
      patch: typeof patch === "string" ? patch : undefined,
      additions: meta?.filediff?.additions ?? counts.additions,
      deletions: meta?.filediff?.deletions ?? counts.deletions,
    };
  }
  return undefined;
}

export function resolveToolFileDiagnostics(
  part: ToolPart,
  isEdit: boolean,
  file: string
): DiagnosticItem[] {
  if (!isEdit || !file) return [];
  const state = part.state;
  const metaDiag = state.status !== "pending" ? state.metadata?.diagnostics : undefined;
  if (metaDiag && typeof metaDiag === "object") {
    for (const [k, list] of Object.entries(metaDiag)) {
      if (
        (k === file || k.endsWith(`/${file}`) || file.endsWith(`/${k}`)) &&
        Array.isArray(list)
      ) {
        return list as DiagnosticItem[];
      }
    }
  }
  return [];
}
