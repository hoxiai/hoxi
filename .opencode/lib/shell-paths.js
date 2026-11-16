// One parsed cd argument, without reconstructing shell text. Mirrors shell_paths.go.
import { scanShellWordSpans, unquoteLiteralShellScriptArg } from "./command-data-args.js";
import { normalizeHomePrefixPath } from "./shell-loops.js";

export function literalCDTarget(segment) {
  const spans = scanShellWordSpans(segment);
  if (!spans || spans.length !== 2 || segment.slice(spans[0].start, spans[0].end) !== "cd") return null;
  const raw = segment.slice(spans[1].start, spans[1].end);
  let target = raw;
  if (raw.startsWith("'") || raw.startsWith('"')) {
    target = unquoteLiteralShellScriptArg(raw);
    if (target === "" && raw !== "''" && raw !== '""') return null;
  } else if (/['"\\]/.test(raw)) return null;
  if (!raw.startsWith("'") && (!raw.startsWith('"') || target.startsWith("$"))) target = normalizeHomePrefixPath(target) || target;
  return !target || target.startsWith("-") || target.startsWith("~") || /[\0\r\n\t$`\\*?\[\]{}]/.test(target) ? null : target;
}
