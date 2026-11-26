// web/src/components/timeline/CodeBlockCard.tsx
import { createEffect, createMemo, createSignal, Index, on, onCleanup, Show } from "solid-js";
import { ChevronDown, ChevronUp, Eye, PanelRightOpen } from "lucide-solid";
import { t } from "../../i18n";
import {
  getCachedHighlight,
  type HighlightedCode,
  stabilizeHighlightedLines,
} from "./syntax-highlight";
import { createSyntaxHighlightClient } from "./syntax-highlight-client";
import {
  codeWrapEnabled,
  isAnyPortalMode,
  isHtmlPreviewCodeBlockLang,
  openCanvasPreview,
  openDocumentPreview,
  toggleCodeWrap,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import { ActionIconButton } from "./tool-card-helpers";
import { useMarkdownPortalMode, useMarkdownStreaming } from "./stream-reveal";

export const CODE_COLLAPSE_THRESHOLD_LINES = 28;
export const CODE_COLLAPSE_PREVIEW_LINES = 18;

const MAX_CODE_EXPAND_ENTRIES = 256;
const codeExpandOverrides = new Map<string, boolean>();

function rememberCodeExpand(key: string, expanded: boolean): void {
  if (codeExpandOverrides.has(key)) codeExpandOverrides.delete(key);
  else if (codeExpandOverrides.size >= MAX_CODE_EXPAND_ENTRIES) {
    const oldest = codeExpandOverrides.keys().next().value;
    if (oldest !== undefined) codeExpandOverrides.delete(oldest);
  }
  codeExpandOverrides.set(key, expanded);
}

function codeBlockKey(lang: string, code: string): string {
  const len = code.length;
  const head = code.slice(0, 48);
  const tail = len > 48 ? code.slice(-32) : "";
  return `${lang}:${len}:${head}:${tail}`;
}

function detectCodeKind(lang: string): "plain" | "shell" | "diff" {
  const clean = lang.trim().toLowerCase();
  if (
    ["sh", "bash", "zsh", "fish", "shell", "shellscript", "console", "powershell", "ps1", "cmd"].includes(
      clean
    )
  ) {
    return "shell";
  }
  if (clean === "diff" || clean === "patch") return "diff";
  return "plain";
}

export function diffLineTone(line: string): "add" | "del" | "hunk" | null {
  if (line.startsWith("@@ ") || line.startsWith("@@\t")) return "hunk";
  if (line.startsWith("+") && !line.startsWith("+++")) return "add";
  if (line.startsWith("-") && !line.startsWith("---")) return "del";
  return null;
}

function diffLineBgClass(tone: "add" | "del" | "hunk" | null): string {
  switch (tone) {
    case "add":
      return "bg-emerald-500/14 px-4";
    case "del":
      return "bg-rose-500/14 px-4";
    case "hunk":
      return "bg-[var(--violet-surface)]/45 text-[var(--violet-text)] px-4";
    default:
      return "px-4";
  }
}

export function streamingHighlightDelayMs(codeLength: number): number {
  if (codeLength >= 8000) return 260;
  if (codeLength >= 2000) return 160;
  return 80;
}

export default function CodeBlockCard(props: {
  lang: string;
  code: string;
  ref?: (el: HTMLDivElement) => void;
}) {
  const streaming = useMarkdownStreaming();
  const markdownPortal = useMarkdownPortalMode();
  const isPortal = () => isAnyPortalMode() || markdownPortal();
  const initialTheme = workspace.theme === "dark" ? "dark" : "light";
  const [copied, setCopied] = createSignal(false);
  const blockKey = createMemo(() => codeBlockKey(props.lang, props.code));
  const [manualExpand, setManualExpand] = createSignal<boolean | undefined>(
    codeExpandOverrides.get(blockKey())
  );
  createEffect(
    on(
      blockKey,
      (key) => {
        setManualExpand(codeExpandOverrides.get(key));
      },
      { defer: true }
    )
  );
  const wrap = codeWrapEnabled;

  const rawLines = createMemo(() => (props.code ? props.code.split("\n") : []));
  const totalLines = () => rawLines().length;
  const canCollapse = createMemo(
    () => !streaming() && totalLines() > CODE_COLLAPSE_THRESHOLD_LINES
  );
  const isCollapsed = createMemo(() => {
    if (!canCollapse()) return false;
    const override = manualExpand();
    return override !== undefined ? !override : true;
  });
  const displayCode = createMemo(() =>
    isCollapsed() ? rawLines().slice(0, CODE_COLLAPSE_PREVIEW_LINES).join("\n") : props.code
  );
  const toggleExpand = () => {
    const nextExpanded = isCollapsed();
    rememberCodeExpand(blockKey(), nextExpanded);
    setManualExpand(nextExpanded);
  };

  const initialCached = getCachedHighlight(displayCode(), props.lang, initialTheme);
  let lastState: (HighlightedCode & { lang: string; theme: string }) | undefined = initialCached
    ? { ...initialCached, lang: props.lang, theme: initialTheme }
    : undefined;
  const [highlighted, setHighlighted] = createSignal<
    (HighlightedCode & { lang: string; theme: string }) | undefined
  >(lastState);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let highlightTimer: ReturnType<typeof setTimeout> | undefined;
  const highlightClient = createSyntaxHighlightClient();
  let inFlight = false;
  let revision = 0;
  let disposed = false;
  let latest = { code: "", lang: "", theme: "dark" as "dark" | "light" };

  const applyHighlight = (
    result: HighlightedCode | undefined,
    lang: string,
    theme: string
  ) => {
    if (!result) {
      lastState = undefined;
      setHighlighted(undefined);
      return;
    }
    const next = {
      code: result.code,
      tokens:
        lastState && lastState.lang === lang && lastState.theme === theme
          ? stabilizeHighlightedLines(lastState.tokens, result.tokens)
          : result.tokens,
      lang,
      theme,
    };
    lastState = next;
    setHighlighted(next);
  };

  const visibleHighlight = createMemo(() => {
    const theme = workspace.theme === "dark" ? "dark" : "light";
    const targetCode = displayCode();
    const asyncRes = highlighted();
    if (
      asyncRes &&
      asyncRes.code === targetCode &&
      asyncRes.lang === props.lang &&
      asyncRes.theme === theme
    ) {
      lastState = asyncRes;
      return asyncRes;
    }
    if (
      lastState &&
      lastState.lang === props.lang &&
      lastState.theme === theme &&
      targetCode.startsWith(lastState.code)
    ) {
      return lastState;
    }
    return undefined;
  });

  createEffect(
    on(
      () => [displayCode(), props.lang, workspace.theme] as const,
      ([targetCode, lang, rawTheme]) => {
        const theme = rawTheme === "dark" ? "dark" : "light";
        latest = { code: targetCode, lang, theme };
        revision++;
        const current = visibleHighlight();
        if (current && current.code === latest.code) {
          if (highlightTimer !== undefined) {
            clearTimeout(highlightTimer);
            highlightTimer = undefined;
          }
          return;
        }
        scheduleHighlight();
      }
    )
  );

  function scheduleHighlight() {
    if (disposed || highlightTimer !== undefined || inFlight) return;
    highlightTimer = setTimeout(runHighlight, streamingHighlightDelayMs(latest.code.length));
  }

  async function runHighlight() {
    highlightTimer = undefined;
    const snapshot = latest;
    const request = revision;
    inFlight = true;
    const result = await highlightClient.highlight(snapshot.code, snapshot.lang, snapshot.theme);
    inFlight = false;
    if (disposed) return;
    if (
      result &&
      snapshot.lang === latest.lang &&
      snapshot.theme === latest.theme &&
      latest.code.startsWith(result.code)
    ) {
      applyHighlight(result, snapshot.lang, snapshot.theme);
    } else if (request === revision) {
      applyHighlight(undefined, snapshot.lang, snapshot.theme);
    }
    if (request !== revision) scheduleHighlight();
  }

  onCleanup(() => {
    disposed = true;
    clearTimeout(highlightTimer);
    clearTimeout(copyTimer);
    highlightClient.dispose();
  });

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(props.code);
      if (disposed) return;
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1800);
    } catch {
      /* ignore */
    }
  };

  const codeKind = () => detectCodeKind(props.lang);
  const isDiff = () => codeKind() === "diff";
  const displayLines = createMemo(() =>
    isCollapsed() ? rawLines().slice(0, CODE_COLLAPSE_PREVIEW_LINES) : rawLines()
  );

  return (
    <div
      ref={props.ref}
      data-code-collapsed={isCollapsed() ? "true" : "false"}
      class={`group/code relative my-1 overflow-hidden rounded-xl border bg-[var(--bg-code)] ${
        isDiff() ? "border-[var(--violet-border)]/60" : "border-[var(--border-subtle)]"
      }`}
    >
      <div class="pointer-events-none absolute right-2 top-2 z-10 flex items-center justify-end gap-1 opacity-0 transition-opacity duration-150 group-hover/code:pointer-events-auto group-hover/code:opacity-100 group-focus-within/code:pointer-events-auto group-focus-within/code:opacity-100">
        <span
          class={`inline-flex items-center gap-1 rounded-md bg-[var(--bg-elevated)]/90 px-1.5 py-0.5 font-mono text-[11px] shadow-sm ${
            codeKind() === "shell"
              ? "text-[var(--ok-text)]"
              : isDiff()
                ? "text-[var(--violet-text)]"
                : "text-[var(--text-muted)]"
          }`}
        >
          <Icon name="code-lines" size="small" />
          <span>{props.lang}</span>
          <Show when={canCollapse()}>
            <span class="text-[var(--text-faint)]">· {t("timeline.codeLinesCount", { count: totalLines() })}</span>
          </Show>
        </span>
        <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--bg-elevated)]/90 p-0.5">
          <Show when={isHtmlPreviewCodeBlockLang(props.lang) && props.code.trim().length > 0}>
            <ActionIconButton
              label={t("timeline.previewHtml")}
              tooltipSide="left"
              onClick={() =>
                openDocumentPreview({
                  kind: "html",
                  title: t("timeline.previewHtml"),
                  html: props.code,
                })
              }
            >
              <Eye size={13} />
            </ActionIconButton>
            <Show when={!isPortal()}>
              <ActionIconButton
                label={t("timeline.dockHtmlPreview")}
                tooltipSide="left"
                onClick={() =>
                  openCanvasPreview({
                    kind: "html",
                    title: t("timeline.previewHtml"),
                    content: props.code,
                  })
                }
              >
                <PanelRightOpen size={13} />
              </ActionIconButton>
            </Show>
          </Show>
          <Show when={canCollapse()}>
            <ActionIconButton
              label={
                isCollapsed()
                  ? t("timeline.codeExpandLines", { count: totalLines() })
                  : t("timeline.codeCollapseLines")
              }
              tooltipSide="left"
              onClick={toggleExpand}
            >
              <Show when={isCollapsed()} fallback={<ChevronUp size={13} />}>
                <ChevronDown size={13} />
              </Show>
            </ActionIconButton>
          </Show>
          <ActionIconButton
            label={wrap() ? t("timeline.codeWrapOff") : t("timeline.codeWrapOn")}
            tooltipSide="left"
            onClick={toggleCodeWrap}
          >
            <Icon
              name="align-right"
              size="small"
              class={wrap() ? "text-[var(--accent-primary)]" : undefined}
            />
          </ActionIconButton>
          <ActionIconButton
            label={copied() ? t("common.copied") : t("common.copy")}
            tooltipSide="left"
            onClick={() => void onCopy()}
          >
            <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
              <Icon name="check" size="small" class="text-[var(--ok-text)]" />
            </Show>
          </ActionIconButton>
        </span>
      </div>

      <div class="relative">
        <pre
          data-scrollable
          class={`overflow-x-auto font-mono leading-[1.65] text-[var(--text-primary)] selectable-text ${
            isDiff() ? "py-4" : "p-4"
          } ${wrap() ? "whitespace-pre-wrap break-words" : "whitespace-pre"}`}
          style={{ "font-size": "var(--app-code-text-size)" }}
        >
          <code class={isDiff() && !wrap() ? "inline-block min-w-full" : undefined}>
            <Show
              when={!isDiff()}
              fallback={
                <Index each={displayLines()}>
                  {(line, index) => {
                    const tone = () => diffLineTone(line());
                    const tokens = () => {
                      const tks = visibleHighlight()?.tokens[index];
                      return tks && tks.length > 0 ? tks : undefined;
                    };
                    return (
                      <span
                        data-diff-tone={tone() ?? undefined}
                        class={`block min-h-[1.65em] ${diffLineBgClass(tone())}`}
                      >
                        <Show when={tokens()} fallback={line() || "\u00A0"}>
                          {(toks) => (
                            <Index each={toks()}>
                              {(token) => (
                                <span
                                  style={{
                                    color: token().color,
                                    "font-style": (token().fontStyle ?? 0) & 1 ? "italic" : undefined,
                                    "font-weight": (token().fontStyle ?? 0) & 2 ? "bold" : undefined,
                                    "text-decoration": (token().fontStyle ?? 0) & 4 ? "underline" : undefined,
                                  }}
                                >
                                  {token().content}
                                </span>
                              )}
                            </Index>
                          )}
                        </Show>
                      </span>
                    );
                  }}
                </Index>
              }
            >
              <Show when={visibleHighlight()} fallback={displayCode() || "\u00A0"}>
                {(result) => (
                  <>
                    <Index each={result().tokens}>
                      {(line, index) => (
                        <>
                          {index > 0 ? "\n" : ""}
                          <Index each={line()}>
                            {(token) => (
                              <span
                                style={{
                                  color: token().color,
                                  "font-style": (token().fontStyle ?? 0) & 1 ? "italic" : undefined,
                                  "font-weight": (token().fontStyle ?? 0) & 2 ? "bold" : undefined,
                                  "text-decoration": (token().fontStyle ?? 0) & 4 ? "underline" : undefined,
                                }}
                              >
                                {token().content}
                              </span>
                            )}
                          </Index>
                        </>
                      )}
                    </Index>
                    {displayCode().slice(result().code.length)}
                  </>
                )}
              </Show>
            </Show>
          </code>
        </pre>

        <Show when={isCollapsed()}>
          <div class="pointer-events-none absolute inset-x-0 bottom-0 h-14 bg-gradient-to-t from-[var(--bg-code)] via-[var(--bg-code)]/80 to-transparent" />
        </Show>
      </div>

      <Show when={canCollapse()}>
        <button
          type="button"
          data-code-expand-btn="true"
          onClick={toggleExpand}
          class="flex w-full items-center justify-center gap-1.5 border-t border-[var(--border-subtle)]/80 bg-[var(--bg-elevated)]/55 px-3 py-1.5 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none"
        >
          <Show when={isCollapsed()} fallback={<ChevronUp size={12} />}>
            <ChevronDown size={12} />
          </Show>
          <span>
            {isCollapsed()
              ? t("timeline.codeExpandLines", { count: totalLines() })
              : t("timeline.codeCollapseLines")}
          </span>
        </button>
      </Show>
    </div>
  );
}
