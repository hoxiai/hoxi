import { createEffect, createMemo, createSignal, Index, Match, onCleanup, Show, Switch } from "solid-js";
import { ExternalLink } from "lucide-solid";
import {
  activeDirectory,
  appSettings,
  checkFile,
  extractFilePathFromHref,
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
  stripFileLineSuffix,
} from "./markdown-parser";
import MarkdownImage from "./MarkdownImage";

function hasNestedInlineSyntax(text: string): boolean {
  return text.includes("`") || /<br\s*\/?>/i.test(text) || text.includes("~~");
}

function isLocalFileHref(href: string): boolean {
  return extractFilePathFromHref(href) !== null;
}

function isHttpUrl(raw: string): boolean {
  return /^https?:\/\/[^\s<>"]+$/i.test(raw.trim());
}

function FileCodeSpan(props: { text: string }) {
  const [verified, setVerified] = createSignal(looksLikeFilePath(props.text));
  const cleanPath = () => stripFileLineSuffix(props.text);

  createEffect(() => {
    const raw = props.text;
    const dir = activeDirectory() || workspace.rootDir || "";
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
        <code class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)]">
          {props.text}
        </code>
      }
    >
      <button
        type="button"
        onClick={() => openFileInInspector(props.text)}
        title={t("timeline.openInInspectorTooltip")}
        class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
      >
        <FileIcon node={{ path: cleanPath(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
        <span>{props.text}</span>
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
  return (
    <Index each={spans()}>
      {(span) => (
        <Switch fallback={<>{(span() as Extract<InlineSpan, { type: "text" }>).text}</>}>
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
              !(span() as Extract<InlineSpan, { type: "code" }>).unclosed &&
              isLikelyFilePath((span() as Extract<InlineSpan, { type: "code" }>).text)
            }
          >
            <FileCodeSpan text={(span() as Extract<InlineSpan, { type: "code" }>).text} />
          </Match>
          <Match
            when={
              span().type === "code" &&
              !(span() as Extract<InlineSpan, { type: "code" }>).unclosed &&
              isHttpUrl((span() as Extract<InlineSpan, { type: "code" }>).text)
            }
          >
            {(_) => {
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
                <span class="inline-flex items-center gap-0.5 align-baseline">
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
                    <span>{codeText()}</span>
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
                  <Show when={hasNestedInlineSyntax(inner())} fallback={inner()}>
                    <MarkdownInline text={inner()} />
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
              const localTarget = () => extractFilePathFromHref(linkSpan().href);
              const isHttp = () => isHttpUrl(linkSpan().href);
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
                  when={!isLocalFileHref(linkSpan().href)}
                  fallback={
                    <button
                      type="button"
                      onClick={() => openFileInInspector(localTarget() || linkSpan().href)}
                      title={t("timeline.openInInspectorTooltip")}
                      class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:border-[var(--accent-border)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[0.88em] text-[var(--text-primary)] hover:text-[var(--accent-text)] transition-colors cursor-pointer align-baseline"
                    >
                      <FileIcon
                        node={{ path: stripFileLineSuffix(localTarget() || linkSpan().href), type: "file" }}
                        class="w-3.5 h-3.5 shrink-0"
                      />
                      <span>{linkSpan().text.replace(/^`|`$/g, "")}</span>
                    </button>
                  }
                >
                  <Show
                    when={isHttp()}
                    fallback={
                      <a
                        href={linkSpan().href}
                        target="_blank"
                        rel="noreferrer"
                        class="text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 break-all cursor-pointer"
                      >
                        <Show when={hasNestedInlineSyntax(linkSpan().text)} fallback={linkSpan().text}>
                          <MarkdownInline text={linkSpan().text} />
                        </Show>
                      </a>
                    }
                  >
                    <span class="inline-flex items-center gap-0.5 align-baseline">
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
                        <Show when={hasNestedInlineSyntax(linkSpan().text)} fallback={linkSpan().text}>
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
              );
            }}
          </Match>
        </Switch>
      )}
    </Index>
  );
}
