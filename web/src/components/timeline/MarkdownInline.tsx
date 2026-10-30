import { createEffect, createMemo, createSignal, Index, Match, onCleanup, Show, Switch } from "solid-js";
import { ExternalLink } from "lucide-solid";
import {
  activeDirectory,
  appSettings,
  checkFile,
  extractFilePathFromHref,
  getCachedFileCheck,
  looksLikeFilePath,
  openExternalURL,
  openFileInInspector,
  openURLInInspector,
  workspace,
} from "../../store/workspace";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import {
  type InlineSpan,
  isLikelyFilePath,
  isSameInlineSpan,
  parseInline,
  resolveMarkdownRelativePath,
  resolveMarkdownFileLink,
  slugifyHeading,
  stripFileLineSuffix,
} from "./markdown-parser";
import MarkdownImage from "./MarkdownImage";
import { StreamRevealText, useInlineRevealRef, useMarkdownBaseFilePath } from "./stream-reveal";

function hasNestedInlineSyntax(text: string): boolean {
  return (
    text.includes("`") ||
    /<br\s*\/?>/i.test(text) ||
    text.includes("~~") ||
    /\*\*[^*\n]+\*\*|__[^_\n]+__|\*[^*\n]+\*|\[[^\]\n]+\]\([^)\n]*\)/.test(text)
  );
}

function isHttpUrl(raw: string): boolean {
  return /^https?:\/\/[^\s<>"]+$/i.test(raw.trim());
}

function isCodeOrFileLinkLabel(rawLabel: string): boolean {
  const trimmed = rawLabel.trim();
  if (!trimmed) return false;
  if (trimmed.startsWith("`")) return true;
  return isLikelyFilePath(trimmed.replace(/^`|`$/g, ""));
}

function isExternalOrAnchorHref(href: string): boolean {
  const trimmed = href.trim();
  if (!trimmed || trimmed === "#") return false;
  if (trimmed.startsWith("#")) return true;
  if (/^(?:https?|mailto|tel|javascript|vbscript|data|ws|wss):/i.test(trimmed)) return true;
  if (/^(?:h|ht|htt|http|https|mailto)$/i.test(trimmed)) return true;
  return false;
}

function scrollToHeadingAnchor(e: MouseEvent, rawHref: string) {
  const anchor = rawHref.trim().replace(/^#+/, "");
  if (!anchor || typeof document === "undefined") return;
  let decoded = anchor;
  try {
    decoded = decodeURIComponent(anchor);
  } catch {
    /* ignore */
  }
  const slug = slugifyHeading(decoded);
  const currentEl = e.currentTarget as HTMLElement | null;
  const root: ParentNode =
    currentEl?.closest("[data-markdown-reader-root]") ||
    currentEl?.closest(".overflow-auto, .overflow-y-auto") ||
    document;
  const candidates = Array.from(root.querySelectorAll<HTMLElement>("[data-heading-slug]"));
  const target =
    candidates.find((el) => el.dataset.headingSlug === decoded) ||
    candidates.find((el) => el.dataset.headingSlug === slug) ||
    candidates.find((el) => el.id === `hoxi-md-${slug}`);
  if (target) {
    e.preventDefault();
    target.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function FileCodeSpan(props: { text: string; unclosed?: boolean }) {
  const inlineRevealRef = useInlineRevealRef();
  const baseFilePath = useMarkdownBaseFilePath();
  const effectivePath = () => {
    const raw = props.text;
    if (raw.startsWith("./") || raw.startsWith("../")) {
      return resolveMarkdownRelativePath(raw, baseFilePath());
    }
    return raw;
  };
  const resolveSyncVerified = (raw: string) => {
    const dir = activeDirectory() || workspace.rootDir || "";
    const cached = getCachedFileCheck(raw, dir);
    if (cached !== undefined) return cached;
    return looksLikeFilePath(raw);
  };
  const [verified, setVerified] = createSignal(resolveSyncVerified(effectivePath()));
  const cleanPath = () => stripFileLineSuffix(effectivePath());

  createEffect(() => {
    const raw = effectivePath();
    const dir = activeDirectory() || workspace.rootDir || "";
    const cached = getCachedFileCheck(raw, dir);
    if (cached !== undefined) {
      setVerified(cached);
      return;
    }
    if (props.unclosed) {
      setVerified(looksLikeFilePath(raw));
      return;
    }
    const sid = workspace.activeSessionID || undefined;
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });
    // Only query backend when connected or when we have a rootDir
    if (!workspace.connected && !dir) return;
    void checkFile(raw, dir, sid).then((exists) => {
      if (!cancelled) {
        setVerified(exists);
      }
    });
  });

  return (
    <Show
      when={verified()}
      fallback={
        <code
          ref={inlineRevealRef}
          class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)]"
        >
          {props.text}
        </code>
      }
    >
      <button
        ref={inlineRevealRef}
        type="button"
        onClick={() => openFileInInspector(effectivePath())}
        title={t("timeline.openInInspectorTooltip")}
        class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
      >
        <FileIcon node={{ path: cleanPath(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
        <span class="break-all">{props.text}</span>
      </button>
    </Show>
  );
}

export default function MarkdownInline(props: { text: string }) {
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
  // Pass changing text directly as a JSX prop on `<StreamRevealText>` so `<Switch>`
  // never unmounts and recreates the text component on streamed updates.
  return (
    <Index each={spans()}>
      {(span) => (
        <Switch
          fallback={
            <StreamRevealText
              text={(span() as Extract<InlineSpan, { type: "text" }>).text}
            />
          }
        >
          <Match when={span().type === "br"}>
            <br />
          </Match>
          <Match when={span().type === "image"}>
            {(_) => {
              const image = () => span() as Extract<InlineSpan, { type: "image" }>;
              return <MarkdownImage alt={image().alt} src={image().src} title={image().title} />;
            }}
          </Match>
          <Match
            when={
              span().type === "code" &&
              isLikelyFilePath((span() as Extract<InlineSpan, { type: "code" }>).text) &&
              (!(span() as Extract<InlineSpan, { type: "code" }>).unclosed ||
                looksLikeFilePath((span() as Extract<InlineSpan, { type: "code" }>).text) ||
                getCachedFileCheck(
                  (span() as Extract<InlineSpan, { type: "code" }>).text,
                  activeDirectory() || workspace.rootDir || ""
                ) === true)
            }
          >
            <FileCodeSpan
              text={(span() as Extract<InlineSpan, { type: "code" }>).text}
              unclosed={(span() as Extract<InlineSpan, { type: "code" }>).unclosed}
            />
          </Match>
          <Match
            when={
              span().type === "code" &&
              !(span() as Extract<InlineSpan, { type: "code" }>).unclosed &&
              isHttpUrl((span() as Extract<InlineSpan, { type: "code" }>).text)
            }
          >
            {(_) => {
              const inlineRevealRef = useInlineRevealRef();
              const codeText = () => (span() as Extract<InlineSpan, { type: "code" }>).text;
              const handleOpen = (e: MouseEvent) => {
                e.preventDefault();
                const target =
                  e.metaKey || e.ctrlKey
                    ? appSettings.linkOpenTarget === "external"
                      ? "inspector"
                      : "external"
                    : appSettings.linkOpenTarget;
                if (target === "external") {
                  openExternalURL(codeText());
                } else {
                  openURLInInspector(codeText());
                }
              };
              return (
                <span
                  ref={inlineRevealRef}
                  class="inline-flex items-center gap-0.5 align-baseline"
                >
                  <button
                    type="button"
                    onClick={handleOpen}
                    title={
                      appSettings.linkOpenTarget === "external"
                        ? t("timeline.openInExternalBrowserTooltip")
                        : t("timeline.openInBrowserTooltip")
                    }
                    class="inline-flex items-center gap-1 font-mono text-[0.92em] text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 transition-opacity cursor-pointer"
                  >
                    <span class="break-all">{codeText()}</span>
                  </button>
                  <button
                    type="button"
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      openExternalURL(codeText());
                    }}
                    title={t("timeline.openInExternalBrowserDirectTooltip")}
                    aria-label={t("timeline.openInExternalBrowserDirectTooltip")}
                    class="inline-flex items-center justify-center p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <ExternalLink size={11} />
                  </button>
                </span>
              );
            }}
          </Match>
          <Match when={span().type === "code"}>
            {(_) => {
              const inlineRevealRef = useInlineRevealRef();
              return (
                <code
                  ref={inlineRevealRef}
                  class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)]"
                >
                  {(span() as Extract<InlineSpan, { type: "code" }>).text}
                </code>
              );
            }}
          </Match>
          <Match when={span().type === "bold"}>
            {(_) => {
              const inner = () => (span() as Extract<InlineSpan, { type: "bold" }>).text;
              return (
                <strong class="font-semibold text-[var(--text-primary)]">
                  <Show
                    when={hasNestedInlineSyntax(inner())}
                    fallback={<StreamRevealText text={inner()} />}
                  >
                    <MarkdownInline text={inner()} />
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
                  <Show
                    when={hasNestedInlineSyntax(inner())}
                    fallback={<StreamRevealText text={inner()} />}
                  >
                    <MarkdownInline text={inner()} />
                  </Show>
                </em>
              );
            }}
          </Match>
          <Match when={span().type === "strike"}>
            {(_) => {
              const inner = () => (span() as Extract<InlineSpan, { type: "strike" }>).text;
              return (
                <del class="line-through text-[var(--text-muted)]">
                  <Show
                    when={hasNestedInlineSyntax(inner())}
                    fallback={<StreamRevealText text={inner()} />}
                  >
                    <MarkdownInline text={inner()} />
                  </Show>
                </del>
              );
            }}
          </Match>
          <Match when={span().type === "link"}>
            {(_) => {
              const inlineRevealRef = useInlineRevealRef();
              const baseFilePath = useMarkdownBaseFilePath();
              const linkSpan = () => span() as Extract<InlineSpan, { type: "link" }>;
              const labelText = createMemo(() => linkSpan().text.replace(/^`|`$/g, ""));
              const isClosedLink = createMemo(() => {
                const h = linkSpan().href;
                if (h === "#") return false;
                return linkSpan().text === h || text().includes(`](${h})`);
              });
              const isPendingProseLabel = createMemo(
                () => linkSpan().href === "#" && !isCodeOrFileLinkLabel(linkSpan().text)
              );
              const isAnchorHref = createMemo(() => {
                const h = linkSpan().href.trim();
                return h.startsWith("#") && h.length > 1;
              });
              const isLocalFile = createMemo(() => {
                const s = linkSpan();
                if (s.href === "#") return isCodeOrFileLinkLabel(s.text);
                if (extractFilePathFromHref(s.href) !== null) return true;
                if (!isClosedLink() && !isExternalOrAnchorHref(s.href)) return true;
                return false;
              });
              const resolvedLocalClickTarget = () =>
                resolveMarkdownFileLink(linkSpan().href, labelText(), baseFilePath());
              const iconPath = createMemo(() => {
                const cleanLabel = stripFileLineSuffix(labelText());
                if (isLikelyFilePath(cleanLabel)) return cleanLabel;
                if (!isClosedLink()) return cleanLabel;
                const target = extractFilePathFromHref(linkSpan().href);
                return stripFileLineSuffix(target || linkSpan().href || cleanLabel);
              });
              const isHttp = createMemo(() => {
                const h = linkSpan().href.trim();
                return (
                  isHttpUrl(h) ||
                  (!isClosedLink() && /^(?:https?:\/?\/?|h|ht|htt|http|https)$/i.test(h))
                );
              });
              const handleHttpOpen = (e: MouseEvent) => {
                e.preventDefault();
                const target =
                  e.metaKey || e.ctrlKey
                    ? appSettings.linkOpenTarget === "external"
                      ? "inspector"
                      : "external"
                    : appSettings.linkOpenTarget;
                if (target === "external") {
                  openExternalURL(linkSpan().href);
                } else {
                  openURLInInspector(linkSpan().href);
                }
              };
              return (
                <Show
                  when={!isPendingProseLabel()}
                  fallback={<StreamRevealText text={labelText()} />}
                >
                  <Show
                    when={!isLocalFile()}
                    fallback={
                      <button
                        ref={inlineRevealRef}
                        type="button"
                        onClick={() => openFileInInspector(resolvedLocalClickTarget())}
                        title={t("timeline.openInInspectorTooltip")}
                        class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                      >
                        <FileIcon
                          node={{ path: iconPath(), type: "file" }}
                          class="w-3.5 h-3.5 shrink-0"
                        />
                        <span>{labelText()}</span>
                      </button>
                    }
                  >
                    <Show
                      when={isHttp()}
                      fallback={
                        <a
                          ref={inlineRevealRef}
                          href={linkSpan().href}
                          target={isAnchorHref() ? undefined : "_blank"}
                          rel={isAnchorHref() ? undefined : "noreferrer"}
                          onClick={(e) => {
                            if (isAnchorHref()) {
                              scrollToHeadingAnchor(e, linkSpan().href);
                            }
                          }}
                          class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 break-all cursor-pointer"
                        >
                          <Show
                            when={hasNestedInlineSyntax(linkSpan().text)}
                            fallback={<StreamRevealText text={linkSpan().text} />}
                          >
                            <MarkdownInline text={linkSpan().text} />
                          </Show>
                        </a>
                      }
                    >
                      <span
                        ref={inlineRevealRef}
                        class="inline-flex items-center gap-0.5 align-baseline"
                      >
                        <a
                          href={linkSpan().href}
                          target="_blank"
                          rel="noreferrer"
                          title={
                            appSettings.linkOpenTarget === "external"
                              ? t("timeline.openInExternalBrowserTooltip")
                              : t("timeline.openInBrowserTooltip")
                          }
                          onClick={handleHttpOpen}
                          class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 break-all cursor-pointer"
                        >
                          <Show
                            when={hasNestedInlineSyntax(linkSpan().text)}
                            fallback={<StreamRevealText text={linkSpan().text} />}
                          >
                            <MarkdownInline text={linkSpan().text} />
                          </Show>
                        </a>
                        <button
                          type="button"
                          onClick={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                            openExternalURL(linkSpan().href);
                          }}
                          title={t("timeline.openInExternalBrowserDirectTooltip")}
                          aria-label={t("timeline.openInExternalBrowserDirectTooltip")}
                          class="inline-flex items-center justify-center p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                        >
                          <ExternalLink size={11} />
                        </button>
                      </span>
                    </Show>
                  </Show>
                </Show>
              );
            }}
          </Match>
        </Switch>
      )}
    </Index>
  );
}
