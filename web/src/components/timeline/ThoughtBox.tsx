import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import type { MessagePart } from "../../services/sse-types";
import { appSettings, isSessionBusy } from "../../store/workspace";
import { Icon, TextReveal, TextShimmer } from "../icons";
import MarkdownView from "./MarkdownView";
import { t } from "../../i18n";
import { formatElapsedSeconds } from "./duration-label";

type ReasoningPart = Extract<MessagePart, { type: "reasoning" }>;

export interface ReasoningHeading {
  title?: string;
  body: string;
}

function clean(value: string): string {
  return value
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/<[^>]+>/g, " ")
    .replace(/\*\*[ \t]*\*\*/g, " · ")
    .replace(/[*_~]+/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * Normalizes raw reasoning text from providers and Codex-style summary streams:
 * strips stray `<think>` / `<thinking>` wrappers and splits adjacent bold
 * phase headings (`**Heading 1****Heading 2**`) onto separate paragraph lines.
 */
export function normalizeReasoningText(raw: string): string {
  return (raw ?? "")
    .replace(/\[REDACTED\]/g, "")
    .replace(/<\/?think(?:ing)?>/gi, "")
    .replace(/<\/(?:t(?:h(?:i(?:n(?:k(?:i(?:n(?:g)?)?)?)?)?)?)?)?$/i, "")
    .replace(/([^\s*])\*\*[ \t]*\*\*(?=[^\s*])/g, "$1**\n\n**")
    .replace(/([^\s*])\*\*[ \t]*\*\*$/g, "$1**")
    .replace(/\r\n?/g, "\n")
    .trim();
}

function visible(value: string): string {
  const closed = value.replace(/<!--[\s\S]*?-->/g, "");
  const start = closed.indexOf("<!--");
  if (start === -1) return closed.trim() ? value : "";
  const body = `${closed.slice(0, start)}${closed.slice(start + 4)}`;
  return body.trim() ? body.trimStart() : "";
}

function pick(src: string, expr: RegExp, group = 1): ReasoningHeading | undefined {
  const found = src.match(expr);
  const raw = found?.[group];
  if (!found || !raw) return undefined;

  const title = clean(raw);
  if (!title) return undefined;

  const body = src.slice(found[0].length).trimStart();
  return {
    title,
    body: visible(body),
  };
}

/**
 * Ported from @kilocode/kilo-ui/src/components/reasoning-heading.ts
 * Extracts markdown headings (`### ...`, `**...**`) from reasoning text
 * so the Thought header shows the live reasoning phase title.
 */
export function reasoningHeading(text: string, partial = false): ReasoningHeading {
  const src = normalizeReasoningText(text);
  if (partial && !src.includes("\n")) {
    const mark = src.startsWith("**") ? "**" : src.startsWith("__") ? "__" : "";
    if (mark && src.length > mark.length && !src.slice(mark.length).includes(mark)) {
      return {
        title: clean(src.slice(mark.length)),
        body: "",
      };
    }
  }

  return (
    pick(src, /^<h[1-6][^>]*>([\s\S]*?)<\/h[1-6]>[ \t]*(?:\n|$)?/i) ??
    pick(src, /^#{1,6}[ \t]+([^\n]+?)(?:[ \t]+#+[ \t]*)?(?:\n|$)/) ??
    pick(src, /^([^\n]+)\n(?:=+|-+)[ \t]*(?:\n|$)/) ??
    pick(src, /^(\*\*|__)([^\n]+?)\1[ \t]*(?:\n|$)/, 2) ?? {
      body: visible(src),
    }
  );
}

export function reasoningSummary(body: string): string {
  const line = body
    .split("\n")
    .map((item) => item.trim())
    .find((item) => item.length > 0);
  if (!line) return "";

  const text = clean(line);
  if (!text) return "";

  const end = text.search(/[.!?](?:\s|$)/);
  if (end !== -1 && end + 1 <= 85) return text.slice(0, end + 1);
  if (text.length <= 85) return text;

  const head = text.slice(0, 85).trimEnd();
  const cut = head.lastIndexOf(" ");
  return `${(cut > 0 ? head.slice(0, cut) : head).trimEnd()}…`;
}

/**
 * Extracts a live single-line ticker from streaming or completed reasoning text.
 * While streaming, prefers the most recent section heading (`### ...` or `**...**`)
 * or falls back to the latest non-empty thought line so the single-line header
 * continuously reflects current thinking progress without expanding a 240px box.
 */
export function liveReasoningTicker(rawText: string, isStreaming = false): string {
  const raw = rawText ?? "";
  if (!raw) return "";

  if (!isStreaming) {
    const src = normalizeReasoningText(raw.slice(0, 1024));
    if (!src) return "";
    const parsed = reasoningHeading(src.slice(0, 480), false);
    return parsed.title || reasoningSummary(parsed.body);
  }

  const tail = normalizeReasoningText(raw.slice(-1200)).slice(-600);
  if (!tail) return "";
  const lines = tail
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith("```"));

  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i];
    const atx = line.match(/^#{1,6}[ \t]+(.+?)(?:[ \t]+#+)?$/);
    if (atx?.[1]) {
      const title = clean(atx[1]);
      if (title) return reasoningSummary(title);
    }
    const bold = line.match(/^(\*\*|__)([^*_]+?)\1:?$/);
    if (bold?.[2]) {
      const title = clean(bold[2]);
      if (title) return reasoningSummary(title);
    }
    if (i === lines.length - 1 && (line.startsWith("**") || line.startsWith("__"))) {
      const partialTitle = clean(line.slice(2));
      if (partialTitle) return reasoningSummary(partialTitle);
    }
  }

  const headSrc = normalizeReasoningText(raw.slice(0, 720));
  const headTitle = reasoningHeading(headSrc.slice(0, 360), true).title;
  if (headTitle) return reasoningSummary(headTitle);

  const lastLine = lines[lines.length - 1];
  return lastLine ? reasoningSummary(lastLine) : "";
}

// Persistent open/collapse state across reactive updates (ported from kilocode)
const userOpened = new Set<string>();
const userCollapsed = new Set<string>();

export default function ThoughtBox(props: { part: ReasoningPart; revealKey?: string }) {
  const streaming = () =>
    props.part.time?.end == null &&
    (!props.part.sessionID || isSessionBusy(props.part.sessionID));
  const hasContent = createMemo(() => {
    const raw = props.part.text ?? "";
    if (!raw) return false;
    return (
      normalizeReasoningText(raw.slice(0, 512)).length > 0 ||
      normalizeReasoningText(raw).length > 0
    );
  });

  const resolveDefaultOpen = () => {
    const id = props.part.id;
    if (userOpened.has(id)) return true;
    if (userCollapsed.has(id)) return false;
    // Only open full box by default when the user explicitly selects "expanded";
    // in default "auto-collapse" mode, keep the compact single-line live ticker
    // so transitioning from thinking to answer causes zero height collapse.
    return appSettings.reasoningDisplay === "expanded";
  };

  const [open, setOpen] = createSignal(resolveDefaultOpen());
  const [everOpened, setEverOpened] = createSignal(open());
  const [animateToggle, setAnimateToggle] = createSignal(false);
  let bodyRef: HTMLDivElement | undefined;
  let innerRef: HTMLDivElement | undefined;
  let userScrolledUp = false;
  let lastScrollTop = 0;
  let followFrame: number | undefined;

  const stopFollow = () => {
    if (followFrame !== undefined) {
      cancelAnimationFrame(followFrame);
      followFrame = undefined;
    }
  };

  const maxScrollTop = () =>
    bodyRef ? Math.max(0, bodyRef.scrollHeight - bodyRef.clientHeight) : 0;

  const tickFollow = () => {
    followFrame = undefined;
    if (!streaming() || !open() || userScrolledUp || !bodyRef) return;
    const target = maxScrollTop();
    const rest = target - bodyRef.scrollTop;
    if (Math.abs(rest) < 0.5) {
      bodyRef.scrollTop = target;
      return;
    }
    bodyRef.scrollTop += rest * 0.25;
    followFrame = requestAnimationFrame(tickFollow);
  };

  createEffect(() => {
    if (open()) setEverOpened(true);
  });

  createEffect(() => {
    if (!props.revealKey) return;
    const id = props.part.id;
    if (id) {
      userCollapsed.delete(id);
      userOpened.add(id);
    }
    setAnimateToggle(true);
    setOpen(true);
  });

  // Respect user's global reasoningDisplay setting unless manually toggled on this card
  createEffect(() => {
    const id = props.part.id;
    const mode = appSettings.reasoningDisplay;
    if (userOpened.has(id) || userCollapsed.has(id)) return;
    setAnimateToggle(false);
    setOpen(mode === "expanded");
  });

  // Observe inner reasoning body height (KiloCode pattern): smooth lerp while streaming,
  // synchronous pre-paint snap when settled.
  createEffect(() => {
    if (!everOpened() || !innerRef) return;
    const observer = new ResizeObserver(() => {
      if (!open() || userScrolledUp || !bodyRef) return;
      if (streaming()) {
        if (followFrame === undefined) {
          followFrame = requestAnimationFrame(tickFollow);
        }
        return;
      }
      bodyRef.scrollTop = maxScrollTop();
    });
    observer.observe(innerRef);
    onCleanup(() => observer.disconnect());
  });

  onCleanup(() => {
    stopFollow();
  });

  const onViewportScroll = (e: Event) => {
    const el = e.currentTarget as HTMLDivElement;
    const top = el.scrollTop;
    if (el.scrollHeight - el.clientHeight - top < 10) {
      userScrolledUp = false;
    } else if (top < lastScrollTop - 1) {
      userScrolledUp = true;
    }
    lastScrollTop = top;
  };

  const onViewportWheel = (e: WheelEvent) => {
    if (e.deltaY < 0) {
      userScrolledUp = true;
      stopFollow();
    }
  };

  const headerTitle = createMemo(() => {
    if (open() && !streaming()) {
      return reasoningHeading((props.part.text ?? "").slice(0, 360), false).title || "";
    }
    if (appSettings.reasoningDisplay === "collapsed" && streaming()) {
      return "";
    }
    return liveReasoningTicker(props.part.text ?? "", streaming());
  });

  const bodyContent = createMemo<string>((prev) => {
    if (!open()) return prev ?? "";
    const full = normalizeReasoningText(props.part.text ?? "");
    const parsed = reasoningHeading(full, streaming());
    return parsed.body || full;
  }, "");

  const durationSec = () => {
    const t = props.part.time;
    if (!t || t.end == null) return 0;
    return Math.max(0, (t.end - t.start) / 1000);
  };

  const toggleOpen = () => {
    const id = props.part.id;
    const next = !open();
    if (next) {
      userCollapsed.delete(id);
      userOpened.add(id);
    } else {
      userOpened.delete(id);
      userCollapsed.add(id);
    }
    setAnimateToggle(true);
    setOpen(next);
  };

  return (
    <Show when={hasContent() || streaming()}>
      <div>
        <button
          type="button"
          class="group/trigger inline-flex max-w-full items-center gap-2 rounded-lg py-1.5 text-[13px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          onClick={toggleOpen}
        >
          <span
            class={
              streaming()
                ? "shrink-0 text-[var(--accent-primary)] inline-flex items-center"
                : "shrink-0 text-[var(--text-muted)] group-hover/trigger:text-[var(--text-primary)] inline-flex items-center"
            }
          >
            <Icon name="brain" size="small" />
          </span>
          <span class={streaming() ? "shrink-0 text-[var(--accent-primary)] font-medium" : "shrink-0 font-medium"}>
            <TextShimmer
              text={streaming() ? t("timeline.thinking") : t("timeline.thought")}
              active={streaming()}
            />
          </span>
          <Show when={headerTitle()}>
            <span class="truncate text-[13px] font-normal text-[var(--text-muted)] max-w-[540px] inline-flex items-center gap-1 min-w-0">
              <span class="shrink-0">·</span>
              <TextReveal text={headerTitle()} truncate />
            </span>
          </Show>
          <Show when={!streaming() && durationSec() >= 0.05}>
            <span class="shrink-0 font-mono text-[12px] tabular-nums text-[var(--text-faint)]">
              {formatElapsedSeconds(durationSec())}
            </span>
          </Show>
          <span
            class="hoxi-chevron shrink-0 text-[var(--text-faint)]"
            data-open={open() ? "true" : "false"}
          >
            <Icon name="chevron-right" size="small" />
          </span>
        </button>

        <Show when={everOpened()}>
          <div
            class={`grid ${
              animateToggle()
                ? "transition-[grid-template-rows,opacity,margin-top] duration-200 ease-out"
                : ""
            } ${
              open()
                ? "mt-1.5 grid-rows-[1fr] opacity-100"
                : "mt-0 grid-rows-[0fr] opacity-0 pointer-events-none"
            }`}
            aria-hidden={!open()}
          >
            <div class="min-h-0 overflow-hidden">
              <div
                ref={bodyRef}
                data-scrollable
                onScroll={onViewportScroll}
                onWheel={onViewportWheel}
                class={`max-h-[240px] overflow-y-auto border-l-2 border-[var(--border-subtle)] px-3.5 py-2 text-[13px] leading-relaxed text-[var(--text-secondary)] selectable-text ${
                  streaming() ? "[scrollbar-width:none] [&::-webkit-scrollbar]:hidden" : ""
                }`}
              >
                <div ref={innerRef}>
                  <MarkdownView content={bodyContent()} />
                </div>
              </div>
            </div>
          </div>
        </Show>
      </div>
    </Show>
  );
}
