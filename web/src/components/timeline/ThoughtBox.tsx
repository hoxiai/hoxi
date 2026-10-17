import { createEffect, createMemo, createSignal, Show } from "solid-js";
import type { MessagePart } from "../../services/sse-types";
import { appSettings } from "../../store/workspace";
import { Icon, TextShimmer } from "../icons";
import MarkdownView from "./MarkdownView";
import { t } from "../../i18n";

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
    .replace(/[*_~]+/g, "")
    .replace(/\s+/g, " ")
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
  const src = text.replace(/\r\n?/g, "\n").trim();
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

// Persistent open/collapse state across reactive updates (ported from kilocode)
const userOpened = new Set<string>();
const userCollapsed = new Set<string>();

export default function ThoughtBox(props: { part: ReasoningPart }) {
  const streaming = () => props.part.time?.end == null;
  const rawText = () => (props.part.text ?? "").replace(/\[REDACTED\]/g, "").trim();

  const resolveDefaultOpen = () => {
    const id = props.part.id;
    if (userOpened.has(id)) return true;
    if (userCollapsed.has(id)) return false;
    const mode = appSettings.reasoningDisplay;
    if (mode === "expanded") return true;
    if (mode === "collapsed") return false;
    return streaming();
  };

  const [open, setOpen] = createSignal(resolveDefaultOpen());
  let bodyRef: HTMLDivElement | undefined;

  // Respect user's global reasoningDisplay setting unless manually toggled on this card
  createEffect(() => {
    const id = props.part.id;
    const mode = appSettings.reasoningDisplay;
    const isStreaming = streaming();
    if (userOpened.has(id) || userCollapsed.has(id)) return;
    if (mode === "expanded") {
      setOpen(true);
    } else if (mode === "collapsed") {
      setOpen(false);
    } else {
      setOpen(isStreaming);
    }
  });

  // Auto-scroll reasoning viewport while tokens stream in
  createEffect(() => {
    void rawText();
    if (open() && streaming() && bodyRef) {
      requestAnimationFrame(() => {
        if (bodyRef) bodyRef.scrollTop = bodyRef.scrollHeight;
      });
    }
  });

  const view = createMemo(() => reasoningHeading(rawText(), streaming()));

  const headerTitle = createMemo(() => {
    const t = view().title;
    if (t) return t;
    if (!streaming() && !open()) return reasoningSummary(view().body);
    return "";
  });

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
    setOpen(next);
  };

  return (
    <Show when={rawText().length > 0 || streaming()}>
      <div>
        <button
          type="button"
          class="group/trigger inline-flex max-w-full items-center gap-2 rounded-lg py-1 text-[13px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
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
            <span class="truncate text-[12.5px] font-normal text-[var(--text-muted)] max-w-[540px] inline-flex items-center gap-1">
              <span>·</span>
              <TextShimmer text={headerTitle()} active={streaming()} offset={9} />
            </span>
          </Show>
          <Show when={!streaming() && durationSec() > 0}>
            <span class="shrink-0 font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
              {durationSec().toFixed(1)}s
            </span>
          </Show>
          <span class="shrink-0 text-[var(--text-faint)] inline-flex items-center">
            <Icon name={open() ? "chevron-down" : "chevron-right"} size="small" />
          </span>
        </button>

        <Show when={open()}>
          <div
            ref={bodyRef}
            class="mt-1.5 max-h-[220px] overflow-y-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3 text-[12.5px] leading-relaxed text-[var(--text-secondary)] selectable-text"
          >
            <MarkdownView content={view().body || rawText()} />
          </div>
        </Show>
      </div>
    </Show>
  );
}
