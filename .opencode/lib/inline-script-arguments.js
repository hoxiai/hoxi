// Mirrors inline_script_arguments.go; argv is never reconstructed as shell code.
export function attachedInlineCode(family, word) {
  let prefixes = [];
  if (family === "node" || family === "bun") {
    if (word === "-pe" || word === "-ep") return null;
    prefixes = ["--eval=", "--print=", "-e", "-p"];
  } else if (family === "python" || family === "python3") prefixes = ["-c"];
  for (const prefix of prefixes) {
    if (word.startsWith(prefix) && (word.length > prefix.length || prefix.endsWith("="))) return { prefix, code: word.slice(prefix.length) };
  }
  return null;
}

export function inlineScriptTrailingArgsSafe(seg, family, spans, start, safeNodeFlags) {
  let positional = family !== "node";
  for (let k = start; k < spans.length; k++) {
    const w = spans[k].word;
    if (/^(?:\d*|&)>>?\/dev\/null$|^\d*>&[12]$/.test(w)) continue;
    if (/^(?:\d*|&)>>?$/.test(w) && spans[k + 1]?.word === "/dev/null") { k++; continue; }
    if (/[$`<>]/.test(seg.slice(spans[k].start, spans[k].end))) return false;
    if (!positional && w === "--") { positional = true; continue; }
    if (!positional && w.startsWith("-")) {
      if (!safeNodeFlags.has(w)) return false;
      continue;
    }
    positional = true;
  }
  return true;
}
