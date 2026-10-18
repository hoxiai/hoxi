import { createMemo, Index, Match, Show, Switch } from "solid-js";
import { openFileInInspector, openURLInInspector } from "../../store/workspace";
import { FileIcon, Icon } from "../icons";
import { t } from "../../i18n";
import CodeBlockCard from "./CodeBlockCard";
import {
  type AlertKind,
  type BlockToken,
  type InlineSpan,
  isLikelyFilePath,
  isSameBlockToken,
  isSameInlineSpan,
  type ListItemEntry,
  parseInline,
  parseMarkdownBlocks,
  splitTableRow,
  stripFileLineSuffix,
  type TableAlign,
} from "./markdown-parser";

export {
  type AlertKind,
  type BlockToken,
  isLikelyFilePath,
  type ListItemEntry,
  parseInline,
  parseMarkdownBlocks,
  splitTableRow,
  type TableAlign,
};

function hasNestedInlineSyntax(text: string): boolean {
  return text.includes("`") || /<br\s*\/?>/i.test(text) || text.includes("~~");
}

function isLocalFileHref(href: string): boolean {
  const trimmed = href.trim();
  if (/^file:\/\//i.test(trimmed)) return true;
  if (/^[a-z]+:\/\//i.test(trimmed) || trimmed.startsWith("#")) return false;
  return isLikelyFilePath(trimmed);
}

function isHttpUrl(raw: string): boolean {
  return /^https?:\/\/[^\s<>"]+$/i.test(raw.trim());
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
        <Switch fallback={<>{(span() as Extract<InlineSpan, { type: "text" }>).text}</>}>
          <Match when={span().type === "br"}>
            <br />
          </Match>
          <Match
            when={
              span().type === "code" &&
              isLikelyFilePath((span() as Extract<InlineSpan, { type: "code" }>).text)
            }
          >
            {(_) => {
              const codeText = () => (span() as Extract<InlineSpan, { type: "code" }>).text;
              const cleanPath = () => stripFileLineSuffix(codeText());
              return (
                <button
                  type="button"
                  onClick={() => openFileInInspector(codeText())}
                  title={t("timeline.openInInspectorTooltip")}
                  class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                >
                  <FileIcon node={{ path: cleanPath(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                  <span>{codeText()}</span>
                </button>
              );
            }}
          </Match>
          <Match
            when={
              span().type === "code" &&
              isHttpUrl((span() as Extract<InlineSpan, { type: "code" }>).text)
            }
          >
            {(_) => {
              const codeText = () => (span() as Extract<InlineSpan, { type: "code" }>).text;
              return (
                <button
                  type="button"
                  onClick={() => openURLInInspector(codeText())}
                  title={t("timeline.openInBrowserTooltip")}
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--accent-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                >
                  <span>{codeText()}</span>
                </button>
              );
            }}
          </Match>
          <Match when={span().type === "code"}>
            <code class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)]">
              {(span() as Extract<InlineSpan, { type: "code" }>).text}
            </code>
          </Match>
          <Match when={span().type === "bold"}>
            {(_) => {
              const inner = () => (span() as Extract<InlineSpan, { type: "bold" }>).text;
              return (
                <strong class="font-semibold text-[var(--text-primary)]">
                  <Show when={hasNestedInlineSyntax(inner())} fallback={inner()}>
                    <InlineRenderer text={inner()} />
                  </Show>
                </strong>
              );
            }}
          </Match>
          <Match when={span().type === "italic"}>
            {(_) => {
              const inner = () => (span() as Extract<InlineSpan, { type: "italic" }>).text;
              return (
                <em class="italic">
                  <Show when={hasNestedInlineSyntax(inner())} fallback={inner()}>
                    <InlineRenderer text={inner()} />
                  </Show>
                </em>
              );
            }}
          </Match>
          <Match when={span().type === "strike"}>
            <del class="line-through text-[var(--text-muted)]">
              {(span() as Extract<InlineSpan, { type: "strike" }>).text}
            </del>
          </Match>
          <Match when={span().type === "link"}>
            {(_) => {
              const linkSpan = () => span() as Extract<InlineSpan, { type: "link" }>;
              return (
                <Show
                  when={!isLocalFileHref(linkSpan().href)}
                  fallback={
                    <button
                      type="button"
                      onClick={() => openFileInInspector(linkSpan().href)}
                      title={t("timeline.openInInspectorTooltip")}
                      class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                    >
                      <FileIcon
                        node={{ path: stripFileLineSuffix(linkSpan().href), type: "file" }}
                        class="w-3.5 h-3.5 shrink-0"
                      />
                      <span>{linkSpan().text.replace(/^`|`$/g, "")}</span>
                    </button>
                  }
                >
                  <a
                    href={linkSpan().href}
                    target="_blank"
                    rel="noreferrer"
                    title={isHttpUrl(linkSpan().href) ? t("timeline.openInBrowserTooltip") : undefined}
                    onClick={(e) => {
                      if (isHttpUrl(linkSpan().href) && !e.metaKey && !e.ctrlKey) {
                        e.preventDefault();
                        openURLInInspector(linkSpan().href);
                      }
                    }}
                    class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 break-all cursor-pointer"
                  >
                    <Show when={hasNestedInlineSyntax(linkSpan().text)} fallback={linkSpan().text}>
                      <InlineRenderer text={linkSpan().text} />
                    </Show>
                  </a>
                </Show>
              );
            }}
          </Match>
        </Switch>
      )}
    </Index>
  );
}

function alignClass(align?: TableAlign): string {
  if (align === "center") return "text-center";
  if (align === "right") return "text-right";
  return "text-left";
}

function alertStyle(alert?: AlertKind): { border: string; badgeBg: string; badgeText: string; label: string } {
  switch (alert) {
    case "warning":
    case "caution":
      return {
        border: "border-[var(--warn-border)] bg-[var(--warn-surface)]/40",
        badgeBg: "bg-[var(--warn-surface)] text-[var(--warn-text)]",
        badgeText: "text-[var(--warn-text)]",
        label: alert.toUpperCase(),
      };
    case "tip":
      return {
        border: "border-[var(--ok-border)] bg-[var(--ok-surface)]/35",
        badgeBg: "bg-[var(--ok-surface)] text-[var(--ok-text)]",
        badgeText: "text-[var(--ok-text)]",
        label: "TIP",
      };
    default:
      return {
        border: "border-[var(--accent-border)] bg-[var(--bg-inset)]",
        badgeBg: "bg-[var(--accent-surface)] text-[var(--accent-primary)]",
        badgeText: "text-[var(--accent-primary)]",
        label: (alert || "NOTE").toUpperCase(),
      };
  }
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
            <Match
              when={
                block().type === "heading" &&
                (block() as Extract<BlockToken, { type: "heading" }>).level === 3
              }
            >
              <h3 class="first:mt-0 mt-2.5 -mb-1 text-[1.05em] font-semibold text-[var(--text-primary)]">
                <InlineRenderer text={(block() as Extract<BlockToken, { type: "heading" }>).text} />
              </h3>
            </Match>
            <Match when={block().type === "heading"}>
              <h4 class="first:mt-0 mt-2 -mb-1 text-[0.98em] font-semibold text-[var(--text-secondary)]">
                <InlineRenderer text={(block() as Extract<BlockToken, { type: "heading" }>).text} />
              </h4>
            </Match>
            <Match when={block().type === "list"}>
              {(_) => {
                const listBlock = () => block() as Extract<BlockToken, { type: "list" }>;
                return (
                  <div class="my-0.5 space-y-2 text-[var(--text-primary)]">
                    <Index each={listBlock().items}>
                      {(item) => {
                        const taskMatch = () => item().text.match(/^\[([ xX])\]\s+([\s\S]*)$/);
                        const padLeft = () => `${item().indent * 1.25}rem`;
                        return (
                          <Show
                            when={taskMatch()}
                            fallback={
                              <div
                                class="flex items-start gap-2 leading-[1.68]"
                                style={{ "padding-left": padLeft() }}
                              >
                                <span class="shrink-0 select-none font-mono text-[0.9em] text-[var(--text-muted)] min-w-[1.25rem] text-right">
                                  {item().kind === "ordered"
                                    ? `${item().orderNumber ?? 1}.`
                                    : item().indent > 0
                                      ? "◦"
                                      : "•"}
                                </span>
                                <div class="min-w-0 flex-1 whitespace-pre-wrap break-words">
                                  <InlineRenderer text={item().text} />
                                </div>
                              </div>
                            }
                          >
                            {(tm) => {
                              const checked = () => tm()[1].toLowerCase() === "x";
                              return (
                                <div
                                  class="flex items-start gap-2 leading-[1.68]"
                                  style={{ "padding-left": padLeft() }}
                                >
                                  <span
                                    class={`mt-1 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
                                      checked()
                                        ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                                        : "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-transparent"
                                    }`}
                                  >
                                    <Show when={checked()}>
                                      <Icon name="check-small" size="small" />
                                    </Show>
                                  </span>
                                  <div
                                    class={`min-w-0 flex-1 whitespace-pre-wrap break-words ${
                                      checked() ? "text-[var(--text-secondary)]" : ""
                                    }`}
                                  >
                                    <InlineRenderer text={tm()[2]} />
                                  </div>
                                </div>
                              );
                            }}
                          </Show>
                        );
                      }}
                    </Index>
                  </div>
                );
              }}
            </Match>
            <Match when={block().type === "blockquote"}>
              {(_) => {
                const bq = () => block() as Extract<BlockToken, { type: "blockquote" }>;
                const st = () => alertStyle(bq().alert);
                return (
                  <blockquote
                    class={`my-0.5 border-l-2 px-3.5 py-2.5 text-[0.95em] leading-[1.68] text-[var(--text-secondary)] rounded-r-lg whitespace-pre-wrap break-words ${st().border}`}
                  >
                    <Show when={bq().alert}>
                      <div class="mb-1 flex items-center gap-1.5">
                        <span
                          class={`inline-flex items-center rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold tracking-wider ${st().badgeBg}`}
                        >
                          {st().label}
                        </span>
                      </div>
                    </Show>
                    <InlineRenderer text={bq().text} />
                  </blockquote>
                );
              }}
            </Match>
            <Match when={block().type === "table"}>
              {(_) => {
                const tb = () => block() as Extract<BlockToken, { type: "table" }>;
                return (
                  <div class="my-1 overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
                    <table class="w-full border-collapse text-left text-[0.92em]">
                      <thead>
                        <tr class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]">
                          <Index each={tb().headers}>
                            {(h, colIdx) => (
                              <th
                                class={`px-3 py-2 font-semibold text-[var(--text-primary)] ${alignClass(
                                  tb().aligns[colIdx]
                                )}`}
                              >
                                <InlineRenderer text={h()} />
                              </th>
                            )}
                          </Index>
                        </tr>
                      </thead>
                      <tbody>
                        <Index each={tb().rows}>
                          {(row) => (
                            <tr class="border-b last:border-b-0 border-[var(--border-subtle)]">
                              <Index each={row()}>
                                {(cell, colIdx) => (
                                  <td
                                    class={`px-3 py-2 align-top text-[var(--text-secondary)] ${alignClass(
                                      tb().aligns[colIdx]
                                    )}`}
                                  >
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
                );
              }}
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
