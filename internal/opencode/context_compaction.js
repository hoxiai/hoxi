import {
  EIGHT_DIM_COMPACTION_PROMPT,
  createFrozenProjectMemoryResolver,
} from "../../.opencode/lib/memory-compaction.js";

export { EIGHT_DIM_COMPACTION_PROMPT, createFrozenProjectMemoryResolver };

const VERIFICATION_CMD_RE =
  /\b(go\s+(?:test|vet|build|mod\s+(?:tidy|download))|npm\s+(?:--prefix\s+\S+\s+)?(?:test|install|ci|i|run\s+(?:test|lint|build|check\S*))|pnpm\s+(?:test|install|i|lint|build|check\S*)|yarn\s+(?:test|install|lint|build)|pip3?\s+install|flutter\s+(?:test|analyze|pub\s+get)|pytest|cargo\s+(?:test|check|clippy|fetch)|tsc\b|node\s+--test)\b/i;
const FAILURE_OUTPUT_RE =
  /(?:^|\s)(?:FAIL|--- FAIL:|FAIL\t|AssertionError|panic:|Error:|error TS\d+|ERR!|✖|✗|fatal:)/m;
const ANSI_ESCAPE_RE = /\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))/g;
const BARE_CR_RE = /\r(?!\n)/;

function stripTerminalControlNoise(text) {
  let out = text.replace(ANSI_ESCAPE_RE, "");
  if (BARE_CR_RE.test(out)) {
    out = out
      .split(/\r?\n/)
      .map((line) => {
        if (!line.includes("\r")) return line;
        const segments = line.split("\r").filter((s) => s.length > 0);
        return segments.length > 0 ? segments[segments.length - 1] : "";
      })
      .join("\n");
  }
  return out;
}

export function compactToolOutputForModel(part) {
  if (part?.type !== "tool" || part.tool !== "bash") return null;
  const state = part.state;
  if (!state || state.status !== "completed" || typeof state.output !== "string") return null;
  const rawOut = state.output;
  const out = stripTerminalControlNoise(rawOut);
  if (out.length <= 2400) return out !== rawOut ? out : null;
  const cmd = String(state.input?.command || "");
  const hasFailure = FAILURE_OUTPUT_RE.test(out);

  // 1. Passing test/build/lint output > 2400 chars: keep head + tail summary.
  if (VERIFICATION_CMD_RE.test(cmd) && !hasFailure) {
    const lines = out.split("\n");
    if (lines.length <= 32) return out !== rawOut ? out : null;
    const head = lines.slice(0, 12).join("\n");
    const tail = lines.slice(-15).join("\n");
    const omitted = lines.length - 27;
    return `${head}\n... [${omitted} lines of passing check output omitted] ...\n${tail}`;
  }

  // 2. Oversized command output > 16000 chars: keep head, failure windows, and tail.
  if (out.length > 16000) {
    const lines = out.split("\n");
    if (lines.length <= 120) return out !== rawOut ? out : null;
    const headCount = 30;
    const tailCount = 40;
    const midEnd = lines.length - tailCount;
    const keepMid = new Set();
    for (let i = headCount; i < midEnd; i++) {
      if (FAILURE_OUTPUT_RE.test(lines[i])) {
        const from = Math.max(headCount, i - 2);
        const to = Math.min(midEnd - 1, i + 8);
        for (let k = from; k <= to; k++) keepMid.add(k);
        if (keepMid.size >= 60) break;
      }
    }
    const midLines = [...keepMid].sort((a, b) => a - b).map((idx) => lines[idx]);
    const omitted = lines.length - headCount - tailCount - midLines.length;
    if (omitted <= 20) return out !== rawOut ? out : null;
    const sections = [lines.slice(0, headCount).join("\n")];
    if (midLines.length > 0) {
      sections.push(`... [failure excerpt from middle of log] ...\n${midLines.join("\n")}`);
    }
    sections.push(`... [${omitted} log lines omitted for context budget] ...`);
    sections.push(lines.slice(-tailCount).join("\n"));
    return sections.join("\n");
  }

  return out !== rawOut ? out : null;
}

export function compactMessageHistory(messages) {
  if (!Array.isArray(messages)) return;
  for (const message of messages) {
    const parts = message?.parts;
    if (!Array.isArray(parts)) continue;
    for (let i = 0; i < parts.length; i++) {
      const compacted = compactToolOutputForModel(parts[i]);
      if (compacted !== null) {
        parts[i] = { ...parts[i], state: { ...parts[i].state, output: compacted } };
      }
    }
  }
}
