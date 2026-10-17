import { createMemo, createSignal, For, Show } from "solid-js";
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
  | { type: "link"; label: string; href: string };

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
        spans.push({ type: "link", label: m[1], href: m[2] });
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

function InlineRenderer(props: { text: string }) {
  const spans = createMemo(() => parseInline(props.text));
  return (
    <For each={spans()}>
      {(span) => {
        switch (span.type) {
          case "code":
            if (isLikelyFilePath(span.text)) {
              const cleanPath = span.text.replace(/:\d+(?::\d+)?$/, "");
              return (
                <button
                  type="button"
                  onClick={() => openFileInInspector(span.text)}
                  title={t("timeline.openInInspectorTooltip")}
                  class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.85em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                >
                  <FileIcon node={{ path: cleanPath, type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                  <span>{span.text}</span>
                </button>
              );
            }
            return (
              <code class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.86em] text-[var(--text-primary)]">
                {span.text}
              </code>
            );
          case "bold":
            return <strong class="font-semibold text-[var(--text-primary)]">{span.text}</strong>;
          case "italic":
            return <em class="italic">{span.text}</em>;
          case "link":
            return (
              <a
                href={span.href}
                target="_blank"
                rel="noreferrer"
                class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85"
              >
                {span.label}
              </a>
            );
          default:
            return <>{span.text}</>;
        }
      }}
    </For>
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
      <div class="flex items-center justify-between border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3.5 py-1.5 text-[11px] font-mono text-[var(--text-muted)]">
        <span class="inline-flex items-center gap-1.5">
          <Icon name="code-lines" size="small" class="text-[var(--text-faint)]" />
          {props.lang || "code"}
        </span>
        <button
          type="button"
          onClick={() => void onCopy()}
          class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
          </Show>
          {copied() ? t("common.copied") : t("common.copy")}
        </button>
      </div>
      <pre class="overflow-x-auto p-3.5 font-mono text-[12.5px] leading-[1.6] text-[var(--text-primary)] selectable-text">
        <code>{props.code}</code>
      </pre>
    </div>
  );
}

export default function MarkdownView(props: { content: string }) {
  const blocks = createMemo(() => parseMarkdownBlocks(props.content || ""));

  return (
    <div class="flex flex-col gap-y-3.5 text-[14px] leading-[1.72] text-[var(--text-primary)] selectable-text">
      <For each={blocks()}>
        {(block) => {
          switch (block.type) {
            case "code":
              return <CodeBlockCard lang={block.lang} code={block.code} />;
            case "heading":
              if (block.level === 1) {
                return (
                  <h1 class="first:mt-0 mt-3.5 -mb-1 text-[18px] font-semibold tracking-tight text-[var(--text-primary)]">
                    <InlineRenderer text={block.text} />
                  </h1>
                );
              }
              if (block.level === 2) {
                return (
                  <h2 class="first:mt-0 mt-3 -mb-1 text-[16px] font-semibold tracking-tight text-[var(--text-primary)]">
                    <InlineRenderer text={block.text} />
                  </h2>
                );
              }
              return (
                <h3 class="first:mt-0 mt-2.5 -mb-1 text-[14.5px] font-semibold text-[var(--text-primary)]">
                  <InlineRenderer text={block.text} />
                </h3>
              );
            case "ul":
              return (
                <ul class="my-0.5 list-disc space-y-2 pl-5 text-[var(--text-primary)]">
                  <For each={block.items}>
                    {(item) => (
                      <li class="leading-[1.68]">
                        <InlineRenderer text={item} />
                      </li>
                    )}
                  </For>
                </ul>
              );
            case "ol":
              return (
                <ol class="my-0.5 list-decimal space-y-2 pl-5 text-[var(--text-primary)]">
                  <For each={block.items}>
                    {(item) => (
                      <li class="leading-[1.68]">
                        <InlineRenderer text={item} />
                      </li>
                    )}
                  </For>
                </ol>
              );
            case "blockquote":
              return (
                <blockquote class="my-0.5 border-l-2 border-[var(--accent-border)] bg-[var(--bg-inset)] px-3.5 py-2.5 text-[13.5px] leading-[1.68] text-[var(--text-secondary)] rounded-r-lg">
                  <InlineRenderer text={block.text} />
                </blockquote>
              );
            case "table":
              return (
                <div class="my-1 overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
                  <table class="w-full border-collapse text-left text-[12.5px]">
                    <thead>
                      <tr class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]">
                        <For each={block.headers}>
                          {(h) => (
                            <th class="px-3 py-2 font-semibold text-[var(--text-primary)]">
                              <InlineRenderer text={h} />
                            </th>
                          )}
                        </For>
                      </tr>
                    </thead>
                    <tbody>
                      <For each={block.rows}>
                        {(row) => (
                          <tr class="border-b last:border-b-0 border-[var(--border-subtle)]">
                            <For each={row}>
                              {(cell) => (
                                <td class="px-3 py-2 text-[var(--text-secondary)]">
                                  <InlineRenderer text={cell} />
                                </td>
                              )}
                            </For>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                </div>
              );
            case "hr":
              return <hr class="my-2 border-[var(--border-subtle)]" />;
            case "paragraph":
              return (
                <p class="whitespace-pre-wrap break-words leading-[1.72]">
                  <InlineRenderer text={block.text} />
                </p>
              );
          }
        }}
      </For>
    </div>
  );
}
