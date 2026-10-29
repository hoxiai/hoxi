import { createMemo, For, Index, Match, Show, Switch } from "solid-js";
import {
  type AlertKind,
  type BlockToken,
  computeHeadingSlugList,
  parseNestedBlockquote,
  type TableAlign,
} from "./markdown-parser";
import { Icon } from "../icons";
import { isSubtaskPlanCodeBlockLang } from "../../store/workspace";
import CodeBlockCard from "./CodeBlockCard";
import InlineSubtaskPlanCard from "./InlineSubtaskPlanCard";
import MarkdownInline from "./MarkdownInline";
import { createRevealBirths, revealBlock } from "./stream-reveal";

function alignClass(align?: TableAlign): string {
  if (align === "center") return "text-center";
  if (align === "right") return "text-right";
  return "text-left";
}

function alertStyle(alert?: AlertKind): { border: string; badgeBg: string; label: string } {
  switch (alert) {
    case "warning":
    case "caution":
      return {
        border: "border-[var(--warn-border)] bg-[var(--warn-surface)]/40",
        badgeBg: "bg-[var(--warn-surface)] text-[var(--warn-text)]",
        label: alert.toUpperCase(),
      };
    case "tip":
      return {
        border: "border-[var(--ok-border)] bg-[var(--ok-surface)]/35",
        badgeBg: "bg-[var(--ok-surface)] text-[var(--ok-text)]",
        label: "TIP",
      };
    default:
      return {
        border: "border-[var(--accent-border)] bg-[var(--bg-inset)]",
        badgeBg: "bg-[var(--accent-surface)] text-[var(--accent-primary)]",
        label: (alert || "NOTE").toUpperCase(),
      };
  }
}

export default function MarkdownBlocks(props: {
  blocks: BlockToken[];
  density?: "chat" | "compact" | "document";
  depth?: number;
}) {
  const depth = () => props.depth ?? 0;
  const compact = () => props.density === "compact";
  const isDoc = () => props.density === "document";
  const blockBirth = createRevealBirths();
  const headingSlugs = createMemo(() => computeHeadingSlugList(props.blocks));

  return (
    <Index each={props.blocks}>
      {(block, blockIdx) => (
        <Switch>
          <Match when={block().type === "frontmatter"}>
            <div
              ref={(el) => revealBlock(el, blockBirth(blockIdx))}
              class="my-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 px-3.5 py-2.5 text-[0.9em]"
            >
              <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 items-baseline">
                <For each={(block() as Extract<BlockToken, { type: "frontmatter" }>).entries}>
                  {(entry) => (
                    <>
                      <dt class="font-mono text-[11px] font-medium text-[var(--text-muted)] select-none">
                        {entry.key}
                      </dt>
                      <dd class="min-w-0 text-[var(--text-secondary)] break-words">
                        <MarkdownInline text={entry.value || "—"} />
                      </dd>
                    </>
                  )}
                </For>
              </dl>
            </div>
          </Match>
          <Match
            when={
              block().type === "code" &&
              isSubtaskPlanCodeBlockLang((block() as Extract<BlockToken, { type: "code" }>).lang)
            }
          >
            <InlineSubtaskPlanCard
              ref={(el) => revealBlock(el, blockBirth(blockIdx))}
              code={(block() as Extract<BlockToken, { type: "code" }>).code}
            />
          </Match>
          <Match when={block().type === "code"}>
            <CodeBlockCard
              ref={(el) => revealBlock(el, blockBirth(blockIdx))}
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
            <h1
              id={headingSlugs()[blockIdx] ? `hoxi-md-${headingSlugs()[blockIdx]}` : undefined}
              data-heading-slug={headingSlugs()[blockIdx]}
              ref={(el) =>
                revealBlock(el, blockBirth(blockIdx), { startOpacity: 0.45, translateY: 2 })
              }
              class={`first:mt-0 mt-4 -mb-1 text-[1.32em] font-semibold tracking-tight text-[var(--text-primary)] scroll-mt-12 ${
                isDoc() ? "border-b border-[var(--border-subtle)] pb-2 mb-0.5" : ""
              }`}
            >
              <MarkdownInline
                text={(block() as Extract<BlockToken, { type: "heading" }>).text || "\u00A0"}
              />
            </h1>
          </Match>
          <Match
            when={
              block().type === "heading" &&
              (block() as Extract<BlockToken, { type: "heading" }>).level === 2
            }
          >
            <h2
              id={headingSlugs()[blockIdx] ? `hoxi-md-${headingSlugs()[blockIdx]}` : undefined}
              data-heading-slug={headingSlugs()[blockIdx]}
              ref={(el) =>
                revealBlock(el, blockBirth(blockIdx), { startOpacity: 0.45, translateY: 2 })
              }
              class={`first:mt-0 mt-3.5 -mb-1 text-[1.16em] font-semibold tracking-tight text-[var(--text-primary)] scroll-mt-12 ${
                isDoc() ? "border-b border-[var(--border-subtle)]/70 pb-1.5 mb-0.5" : ""
              }`}
            >
              <MarkdownInline
                text={(block() as Extract<BlockToken, { type: "heading" }>).text || "\u00A0"}
              />
            </h2>
          </Match>
          <Match
            when={
              block().type === "heading" &&
              (block() as Extract<BlockToken, { type: "heading" }>).level === 3
            }
          >
            <h3
              id={headingSlugs()[blockIdx] ? `hoxi-md-${headingSlugs()[blockIdx]}` : undefined}
              data-heading-slug={headingSlugs()[blockIdx]}
              ref={(el) =>
                revealBlock(el, blockBirth(blockIdx), { startOpacity: 0.45, translateY: 2 })
              }
              class="first:mt-0 mt-2.5 -mb-1 text-[1.05em] font-semibold text-[var(--text-primary)] scroll-mt-12"
            >
              <MarkdownInline
                text={(block() as Extract<BlockToken, { type: "heading" }>).text || "\u00A0"}
              />
            </h3>
          </Match>
          <Match when={block().type === "heading"}>
            <h4
              id={headingSlugs()[blockIdx] ? `hoxi-md-${headingSlugs()[blockIdx]}` : undefined}
              data-heading-slug={headingSlugs()[blockIdx]}
              ref={(el) =>
                revealBlock(el, blockBirth(blockIdx), { startOpacity: 0.45, translateY: 2 })
              }
              class="first:mt-0 mt-2 -mb-1 text-[0.98em] font-semibold text-[var(--text-secondary)] scroll-mt-12"
            >
              <MarkdownInline
                text={(block() as Extract<BlockToken, { type: "heading" }>).text || "\u00A0"}
              />
            </h4>
          </Match>
          <Match when={block().type === "list"}>
            {(_) => {
              const listBlock = () => block() as Extract<BlockToken, { type: "list" }>;
              const itemBirth = createRevealBirths();
              return (
                <div class={`my-0.5 text-[var(--text-primary)] ${compact() ? "space-y-1.5" : "space-y-2"}`}>
                  <Index each={listBlock().items}>
                    {(item, itemIdx) => {
                      const taskMatch = () => item().text.match(/^\[([ xX])\]\s+([\s\S]*)$/);
                      const padLeft = () => `${item().indent * 1.25}rem`;
                      return (
                        <Show
                          when={taskMatch()}
                          fallback={
                            <div
                              ref={(el) =>
                                revealBlock(el, itemBirth(itemIdx), {
                                  startOpacity: 0.35,
                                  translateY: 2,
                                })
                              }
                              class="flex items-start gap-2 leading-[1.68]"
                              style={{ "padding-left": padLeft() }}
                            >
                              <span
                                class="shrink-0 select-none font-mono text-[0.9em] text-[var(--text-muted)] min-w-[1.25rem] text-right"
                              >
                                {item().kind === "ordered"
                                  ? `${item().orderNumber ?? 1}.`
                                  : item().indent > 0
                                    ? "◦"
                                    : "•"}
                              </span>
                              <div class="min-w-0 flex-1 whitespace-pre-wrap break-words">
                                <MarkdownInline
                                  text={item().text || "\u00A0"}
                                />
                              </div>
                            </div>
                          }
                        >
                          {(tm) => {
                            const checked = () => tm()[1].toLowerCase() === "x";
                            return (
                              <div
                                ref={(el) =>
                                  revealBlock(el, itemBirth(itemIdx), {
                                    startOpacity: 0.35,
                                    translateY: 2,
                                  })
                                }
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
                                  <MarkdownInline text={tm()[2]} />
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
              const nested = createMemo(() => {
                if (depth() >= 2) return null;
                return parseNestedBlockquote(bq().text);
              });
              return (
                <blockquote
                  ref={(el) => revealBlock(el, blockBirth(blockIdx))}
                  class={`my-0.5 border-l-2 px-3.5 py-2.5 text-[0.95em] leading-[1.68] text-[var(--text-secondary)] rounded-r-lg ${
                    nested() ? "" : "whitespace-pre-wrap break-words"
                  } ${st().border}`}
                >
                  <Show when={bq().alert}>
                    <div class="mb-1 flex items-center gap-1.5">
                      <span
                        class={`inline-flex items-center rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold tracking-wider ${st().badgeBg}`}
                      >
                        {st().label}
                      </span>
                    </div>
                  </Show>
                  <Show
                    when={nested()}
                    fallback={<MarkdownInline text={bq().text} />}
                  >
                    {(inner) => (
                      <div class={`flex flex-col ${compact() ? "gap-y-2" : "gap-y-3"}`}>
                        <MarkdownBlocks
                          blocks={inner()}
                          density="compact"
                          depth={depth() + 1}
                        />
                      </div>
                    )}
                  </Show>
                </blockquote>
              );
            }}
          </Match>
          <Match when={block().type === "table"}>
            {(_) => {
              const tb = () => block() as Extract<BlockToken, { type: "table" }>;
              const tableAt = () => blockBirth(blockIdx);
              const rowBirth = createRevealBirths();
              return (
                <div
                  ref={(el) => revealBlock(el, tableAt())}
                  class="my-1 overflow-x-auto rounded-xl border border-[var(--border-subtle)]"
                >
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
                              <MarkdownInline text={h()} />
                            </th>
                          )}
                        </Index>
                      </tr>
                    </thead>
                    <tbody>
                      <Index each={tb().rows}>
                        {(row, rowIdx) => (
                          <tr
                            ref={(el) => {
                              const at = rowBirth(rowIdx);
                              if (at - tableAt() >= 8) {
                                revealBlock(el, at, { startOpacity: 0, translateY: 2 });
                              }
                            }}
                            class="border-b last:border-b-0 border-[var(--border-subtle)]"
                          >
                            <Index each={row()}>
                              {(cell, colIdx) => (
                                <td
                                  class={`px-3 py-2 align-top text-[var(--text-secondary)] ${alignClass(
                                    tb().aligns[colIdx]
                                  )}`}
                                >
                                  <MarkdownInline text={cell()} />
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
            <hr
              ref={(el) => revealBlock(el, blockBirth(blockIdx))}
              class="my-2 border-[var(--border-subtle)]"
            />
          </Match>
          <Match when={block().type === "paragraph"}>
            <p
              ref={(el) =>
                revealBlock(el, blockBirth(blockIdx), { startOpacity: 0.5, translateY: 2 })
              }
              class="whitespace-pre-wrap break-words leading-[1.7]"
            >
              <MarkdownInline
                text={(block() as Extract<BlockToken, { type: "paragraph" }>).text}
              />
            </p>
          </Match>
        </Switch>
      )}
    </Index>
  );
}
