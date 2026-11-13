import type { MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

export interface TranscriptSearchOptions { query: string; matchCase: boolean; wholeWord: boolean; useRegex: boolean }
export interface SearchTurn { id: string; parts: Array<{ id: string; text?: string; tool?: string; input?: unknown; output?: string }> }
export interface TranscriptMatch { messageID: string; partID?: string }
export interface SearchPattern { source: string; flags: string }
export type SearchRequest = { id: number; pattern: SearchPattern; turns: SearchTurn[] } |
  { id: number; pattern: SearchPattern; texts: string[]; limit: number };
export interface SearchResponse { id: number; matches?: TranscriptMatch[]; ranges?: Array<[number, number, number]> }

export function searchPattern(options: TranscriptSearchOptions): SearchPattern {
  let source = options.useRegex ? options.query.trim() : options.query.trim().replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  if (options.wholeWord) source = `\\b(?:${source})\\b`;
  const flags = options.matchCase ? "g" : "gi";
  new RegExp(source, flags); // Syntax validation only; execution stays in the worker.
  return { source, flags };
}

export function searchPart(part: MessagePart): SearchTurn["parts"][number] {
  if (part.type === "text" || part.type === "reasoning") return { id: part.id, text: part.text };
  if (part.type === "tool") return { id: part.id, tool: part.tool, input: part.state.input,
    output: part.state.status === "completed" ? part.state.output : part.state.status === "error" ? part.state.error : undefined };
  return { id: part.id };
}

function partText(part: SearchTurn["parts"][number]): string {
  const chunks = [part.text ?? "", part.tool ?? ""];
  if (part.input) chunks.push(JSON.stringify(part.input));
  if (part.output) chunks.push(part.output);
  return chunks.filter(Boolean).join("\n");
}

export function matchSearchTurns(turns: SearchTurn[], pattern: SearchPattern): TranscriptMatch[] {
  const regex = new RegExp(pattern.source, pattern.flags);
  const matches: TranscriptMatch[] = [];
  for (const turn of turns) {
    const texts = turn.parts.map(partText);
    regex.lastIndex = 0;
    if (!regex.test(texts.join("\n"))) continue;
    const index = texts.findIndex((text) => { regex.lastIndex = 0; return regex.test(text); });
    matches.push({ messageID: turn.id, partID: turn.parts[index]?.id });
  }
  return matches;
}

export function firstMatchingPartID(turn: MessageTurn, regex: RegExp): string | undefined {
  return matchSearchTurns([{ id: turn.info.id, parts: turn.parts.map(searchPart) }], { source: regex.source, flags: regex.flags })[0]?.partID;
}

export function matchSearchRanges(texts: string[], pattern: SearchPattern, limit: number): Array<[number, number, number]> {
  const regex = new RegExp(pattern.source, pattern.flags);
  const ranges: Array<[number, number, number]> = [];
  for (let i = 0; i < texts.length && ranges.length < limit; i++) {
    regex.lastIndex = 0;
    let match: RegExpExecArray | null;
    while (ranges.length < limit && (match = regex.exec(texts[i]))) {
      if (!match[0].length) { regex.lastIndex++; continue; }
      ranges.push([i, match.index, match.index+match[0].length]);
    }
  }
  return ranges;
}
