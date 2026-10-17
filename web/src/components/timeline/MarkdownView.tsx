import { createMemo, createSignal, For, Index, Match, Show, Switch } from "solid-js";
import { openFileInInspector } from "../../store/workspace";
import { FileIcon, Icon } from "../icons";
import { t } from "../../i18n";

const FILE_PATH_PATTERN =
  /^(?:\.\/)?(?:[\w@-]+\/)*[\w.-]+\.(?:ts|tsx|js|jsx|go|py|rs|md|json|yaml|yml|toml|css|html|sh|sql|vue|svelte|c|cpp|h|java|rb|php)(?::\d+(?::\d+)?)?$/i;

function isLikelyFilePath(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed || trimmed.includes(" ") || trimmed.length > 140) return false;
  return FILE_PATH_PATTERN.test(trimmed);
}

type BlockToken =
  | { type: "code"; lang: string; code: string }
  | { type: "heading"; level: 1 | 2 | 3; text: string }
  | { type: "ul"; items: string[] }
  | { type: "ol"; items: string[] }
  | { type: "blockquote"; text: string }
  | { type: "table"; headers: string[]; rows: string[][] }
  | { type: "hr" }
  | { type: "paragraph"; text: string };

function parseMarkdownBlocks(src: string): BlockToken[] {
  const blocks: BlockToken[] = [];
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block
    const fenceMatch = line.match(/^```([\w.+-]*)\s*$/);
    if (fenceMatch) {
      const lang = fenceMatch[1] || "text";
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // skip closing ```
      blocks.push({ type: "code", lang, code: codeLines.join("\n") });
      continue;
    }

    // Blank line
    if (!line.trim()) {
      i++;
      continue;
    }

    // Horizontal rule
    if (/^(---|\*\*\*|___)\s*$/.test(line.trim())) {
      blocks.push({ type: "hr" });
      i++;
      continue;
    }

    // Heading
    const headingMatch = line.match(/^(#{1,3})\s+(.+)$/);
    if (headingMatch) {
      const level = Math.min(3, headingMatch[1].length) as 1 | 2 | 3;
      blocks.push({ type: "heading", level, text: headingMatch[2].trim() });
      i++;
      continue;
    }

    // GFM Table
    if (
      line.includes("|") &&
      i + 1 < lines.length &&
      /^\s*\|?\s*[-:]+\s*\|/.test(lines[i + 1])
    ) {
      const splitRow = (r: string) =>
        r
          .trim()
          .replace(/^\||\|$/g, "")
          .split("|")
          .map((c) => c.trim());
      const headers = splitRow(line);
      i += 2; // skip header and separator
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") {
        rows.push(splitRow(lines[i]));
        i++;
      }
      blocks.push({ type: "table", headers, rows });
      continue;
    }

    // Blockquote
    if (/^>\s?/.test(line)) {
      const quoteLines: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        quoteLines.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      blocks.push({ type: "blockquote", text: quoteLines.join("\n") });
      continue;
    }

    // Unordered list
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s+/, ""));
        i++;
      }
      blocks.push({ type: "ul", items });
      continue;
    }

    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ""));
        i++;
      }
      blocks.push({ type: "ol", items });
      continue;
    }

    // Regular paragraph (consume until blank line or block starter)
    const paraLines: string[] = [line];
    i++;
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^```/.test(lines[i]) &&
      !/^(#{1,3})\s+/.test(lines[i]) &&
      !/^\s*[-*+]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^>\s?/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    blocks.push({ type: "paragraph", text: paraLines.join("\n") });
  }

  return blocks;
}

type InlineSpan =
  | { type: "text"; text: string }
  | { type: "code"; text: string }
  | { type: "bold"; text: string }
  | { type: "italic"; text: string }
  | { type: "link"; text: string; href: string };

function parseInline(text: string): InlineSpan[] {
  const spans: InlineSpan[] = [];
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\)|\*[^*\n]+\*)/g;
  let lastIdx = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIdx) {
      spans.push({ type: "text", text: text.slice(lastIdx, match.index) });
    }
    const token = match[0];
    if (token.startsWith("`") && token.endsWith("`")) {
      spans.push({ type: "code", text: token.slice(1, -1) });
    } else if (token.startsWith("**") && token.endsWith("**")) {
      spans.push({ type: "bold", text: token.slice(2, -2) });
    } else if (token.startsWith("[")) {
      const m = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      if (m) {
        spans.push({ type: "link", text: m[1], href: m[2] });
      } else {
        spans.push({ type: "text", text: token });
      }
    } else if (token.startsWith("*") && token.endsWith("*")) {
      spans.push({ type: "italic", text: token.slice(1, -1) });
    } else {
      spans.push({ type: "text", text: token });
    }
    lastIdx = regex.lastIndex;
  }

  if (lastIdx < text.length) {
    spans.push({ type: "text", text: text.slice(lastIdx) });
  }
  return spans;
}

function isSameInlineSpan(a: InlineSpan | undefined, b: InlineSpan): boolean {
  if (!a || a.type !== b.type || a.text !== b.text) return false;
  if (a.type === "link" && b.type === "link") return a.href === b.href;
  return true;
}

function isSameStringArray(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

function isSameBlockToken(a: BlockToken | undefined, b: BlockToken): boolean {
  if (!a || a.type !== b.type) return false;
  switch (b.type) {
    case "hr":
      return true;
    case "paragraph":
    case "blockquote":
      return (a as typeof b).text === b.text;
    case "heading":
      return (a as typeof b).level === b.level && (a as typeof b).text === b.text;
    case "code":
      return (a as typeof b).lang === b.lang && (a as typeof b).code === b.code;
    case "ul":
    case "ol":
      return isSameStringArray((a as typeof b).items, b.items);
    case "table": {
      const prev = a as typeof b;
      if (!isSameStringArray(prev.headers, b.headers) || prev.rows.length !== b.rows.length) {
        return false;
      }
      for (let r = 0; r < b.rows.length; r++) {
        if (!isSameStringArray(prev.rows[r], b.rows[r])) return false;
      }
      return true;
    }
  }
}

function InlineRenderer(props: { text: string }) {
  const text = createMemo(() => props.text);
  const spans = createMemo<InlineSpan[]>((prev) => {
    const next = parseInline(text());
    for (let i = 0; i < next.length; i++) {
      if (isSameInlineSpan(prev[i], next[i])) {
        next[i] = prev[i];
      }
    }
    return next;
  }, []);
  return (
    <Index each={spans()}>
      {(span) => (
        <Switch fallback={<>{span().text}</>}>
          <Match when={span().type === "code" && isLikelyFilePath(span().text)}>
            {(_) => {
              const cleanPath = () => span().text.replace(/:\d+(?::\d+)?$/, "");
              return (
                <button
                  type="button"
                  onClick={() => openFileInInspector(span().text)}
                  title={t("timeline.openInInspectorTooltip")}
                  class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                >
                  <FileIcon node={{ path: cleanPath(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                  <span>{span().text}</span>
                </button>
              );
            }}
          </Match>
          <Match when={span().type === "code"}>
            <code class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)]">
              {span().text}
            </code>
          </Match>
          <Match when={span().type === "bold"}>
            <strong class="font-semibold text-[var(--text-primary)]">{span().text}</strong>
          </Match>
          <Match when={span().type === "italic"}>
            <em class="italic">{span().text}</em>
          </Match>
          <Match when={span().type === "link"}>
            <a
              href={(span() as Extract<InlineSpan, { type: "link" }>).href}
              target="_blank"
              rel="noreferrer"
              class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85"
            >
              {span().text}
            </a>
          </Match>
        </Switch>
      )}
    </Index>
  );
}

function CodeBlockCard(props: { lang: string; code: string }) {
  const [copied, setCopied] = createSignal(false);

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(props.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* ignore */
    }
  };

  return (
    <div class="my-1 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-code)]">
      <div class="flex items-center justify-between border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3.5 py-2 text-[12px] font-mono text-[var(--text-muted)]">
        <span class="inline-flex items-center gap-1.5">
          <Icon name="code-lines" size="small" class="text-[var(--text-faint)]" />
          {props.lang || "code"}
        </span>
        <button
          type="button"
          onClick={() => void onCopy()}
          class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
          </Show>
          {copied() ? t("common.copied") : t("common.copy")}
        </button>
      </div>
      <pre
        class="overflow-x-auto p-4 font-mono leading-[1.65] text-[var(--text-primary)] selectable-text"
        style={{ "font-size": "var(--app-code-text-size)" }}
      >
        <code>{props.code}</code>
      </pre>
    </div>
  );
}

export default function MarkdownView(props: { content: string }) {
  const blocks = createMemo<BlockToken[]>((prev) => {
    const next = parseMarkdownBlocks(props.content || "");
    for (let i = 0; i < next.length; i++) {
      if (isSameBlockToken(prev[i], next[i])) {
        next[i] = prev[i];
      }
    }
    return next;
  }, []);

  return (
    <div
      class="flex flex-col gap-y-4 leading-[1.7] text-[var(--text-primary)] selectable-text"
      style={{ "font-size": "var(--app-chat-text-size)" }}
    >
      <Index each={blocks()}>
        {(block) => (
          <Switch>
            <Match when={block().type === "code"}>
              <CodeBlockCard
                lang={(block() as Extract<BlockToken, { type: "code" }>).lang}
                code={(block() as Extract<BlockToken, { type: "code" }>).code}
              />
            </Match>
            <Match
              when={
                block().type === "heading" &&
                (block() as Extract<BlockToken, { type: "heading" }>).level === 1
              }
            >
              <h1 class="first:mt-0 mt-3.5 -mb-1 text-[1.28em] font-semibold tracking-tight text-[var(--text-primary)]">
                <InlineRenderer text={(block() as Extract<BlockToken, { type: "heading" }>).text} />
              </h1>
            </Match>
            <Match
              when={
                block().type === "heading" &&
                (block() as Extract<BlockToken, { type: "heading" }>).level === 2
              }
            >
              <h2 class="first:mt-0 mt-3 -mb-1 text-[1.15em] font-semibold tracking-tight text-[var(--text-primary)]">
                <InlineRenderer text={(block() as Extract<BlockToken, { type: "heading" }>).text} />
              </h2>
            </Match>
            <Match when={block().type === "heading"}>
              <h3 class="first:mt-0 mt-2.5 -mb-1 text-[1.05em] font-semibold text-[var(--text-primary)]">
                <InlineRenderer text={(block() as Extract<BlockToken, { type: "heading" }>).text} />
              </h3>
            </Match>
            <Match when={block().type === "ul"}>
              <ul class="my-0.5 list-disc space-y-2 pl-5 text-[var(--text-primary)]">
                <Index each={(block() as Extract<BlockToken, { type: "ul" }>).items}>
                  {(item) => (
                    <li class="leading-[1.68]">
                      <InlineRenderer text={item()} />
                    </li>
                  )}
                </Index>
              </ul>
            </Match>
            <Match when={block().type === "ol"}>
              <ol class="my-0.5 list-decimal space-y-2 pl-5 text-[var(--text-primary)]">
                <Index each={(block() as Extract<BlockToken, { type: "ol" }>).items}>
                  {(item) => (
                    <li class="leading-[1.68]">
                      <InlineRenderer text={item()} />
                    </li>
                  )}
                </Index>
              </ol>
            </Match>
            <Match when={block().type === "blockquote"}>
              <blockquote class="my-0.5 border-l-2 border-[var(--accent-border)] bg-[var(--bg-inset)] px-3.5 py-2.5 text-[0.95em] leading-[1.68] text-[var(--text-secondary)] rounded-r-lg">
                <InlineRenderer
                  text={(block() as Extract<BlockToken, { type: "blockquote" }>).text}
                />
              </blockquote>
            </Match>
            <Match when={block().type === "table"}>
              <div class="my-1 overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
                <table class="w-full border-collapse text-left text-[0.92em]">
                  <thead>
                    <tr class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]">
                      <Index each={(block() as Extract<BlockToken, { type: "table" }>).headers}>
                        {(h) => (
                          <th class="px-3 py-2 font-semibold text-[var(--text-primary)]">
                            <InlineRenderer text={h()} />
                          </th>
                        )}
                      </Index>
                    </tr>
                  </thead>
                  <tbody>
                    <Index each={(block() as Extract<BlockToken, { type: "table" }>).rows}>
                      {(row) => (
                        <tr class="border-b last:border-b-0 border-[var(--border-subtle)]">
                          <Index each={row()}>
                            {(cell) => (
                              <td class="px-3 py-2 text-[var(--text-secondary)]">
                                <InlineRenderer text={cell()} />
                              </td>
                            )}
                          </Index>
                        </tr>
                      )}
                    </Index>
                  </tbody>
                </table>
              </div>
            </Match>
            <Match when={block().type === "hr"}>
              <hr class="my-2 border-[var(--border-subtle)]" />
            </Match>
            <Match when={block().type === "paragraph"}>
              <p class="whitespace-pre-wrap break-words leading-[1.7]">
                <InlineRenderer
                  text={(block() as Extract<BlockToken, { type: "paragraph" }>).text}
                />
              </p>
            </Match>
          </Switch>
        )}
      </Index>
    </div>
  );
}
