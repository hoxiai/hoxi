// web/src/components/timeline/JsonTreeView.tsx
// Compact, collapsible JSON tree viewer for structured ToolCard outputs/arguments
// with Tree/Raw toggle, Expand/Collapse all, and copy support.
import { createEffect, createMemo, createSignal, For, on, onCleanup, Show } from "solid-js";
import { Check, ChevronRight, Code2, Copy, ListTree } from "lucide-solid";
import { t } from "../../i18n";

const MAX_JSON_PARSE_CHARS = 128 * 1024;
const MAX_VISIBLE_CHILDREN = 80;
const MAX_TREE_DEPTH = 6;

export function tryParseStructuredJson(raw: string): Record<string, unknown> | unknown[] | null {
  const trimmed = (raw || "").trim();
  if (!trimmed || trimmed.length > MAX_JSON_PARSE_CHARS) return null;
  const first = trimmed[0];
  const last = trimmed[trimmed.length - 1];
  if (!((first === "{" && last === "}") || (first === "[" && last === "]"))) return null;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (parsed && typeof parsed === "object") {
      return parsed as Record<string, unknown> | unknown[];
    }
  } catch {
    return null;
  }
  return null;
}

function isExpandable(value: unknown): value is Record<string, unknown> | unknown[] {
  if (!value || typeof value !== "object") return false;
  return Array.isArray(value) ? value.length > 0 : Object.keys(value).length > 0;
}

function formatPrimitive(value: unknown): { text: string; cls: string } {
  if (value === null) return { text: "null", cls: "text-[var(--text-faint)] italic" };
  if (typeof value === "boolean") {
    return { text: String(value), cls: "text-amber-500 dark:text-amber-400" };
  }
  if (typeof value === "number") {
    return { text: String(value), cls: "text-sky-600 dark:text-sky-400" };
  }
  if (typeof value === "string") {
    const display = value.length > 200 ? `${value.slice(0, 200)}…` : value;
    return { text: JSON.stringify(display), cls: "text-emerald-600 dark:text-emerald-400 break-all" };
  }
  if (Array.isArray(value)) return { text: "[]", cls: "text-[var(--text-faint)]" };
  if (typeof value === "object") return { text: "{}", cls: "text-[var(--text-faint)]" };
  return { text: String(value), cls: "text-[var(--text-secondary)]" };
}

function JsonNode(props: {
  keyName?: string;
  value: unknown;
  depth: number;
  expandEpoch: { open: boolean; seq: number };
}) {
  const initialOpen =
    props.expandEpoch.seq > 0 ? props.expandEpoch.open : props.depth < 1;
  const [open, setOpen] = createSignal(initialOpen);

  createEffect(
    on(
      () => props.expandEpoch.seq,
      () => {
        setOpen(props.expandEpoch.open);
      },
      { defer: true }
    )
  );

  const isOpen = () => open();

  const expandable = () => isExpandable(props.value) && props.depth < MAX_TREE_DEPTH;

  const entries = createMemo(() => {
    if (!expandable() || !isOpen()) return [];
    if (Array.isArray(props.value)) {
      return props.value.slice(0, MAX_VISIBLE_CHILDREN).map((v, i) => [String(i), v] as const);
    }
    return Object.entries(props.value as Record<string, unknown>).slice(0, MAX_VISIBLE_CHILDREN);
  });

  const totalCount = () =>
    Array.isArray(props.value)
      ? props.value.length
      : props.value && typeof props.value === "object"
        ? Object.keys(props.value as Record<string, unknown>).length
        : 0;

  return (
    <div class="font-mono text-[11.5px] leading-[1.55]">
      <Show
        when={expandable()}
        fallback={
          <div class="flex items-baseline gap-1.5 pl-4">
            <Show when={props.keyName !== undefined}>
              <span class="shrink-0 text-[var(--violet-text)]">{props.keyName}:</span>
            </Show>
            <span class={formatPrimitive(props.value).cls}>
              {formatPrimitive(props.value).text}
            </span>
          </div>
        }
      >
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          class="flex w-full items-center gap-1 rounded px-0.5 text-left hover:bg-[var(--bg-hover)]/60 cursor-pointer select-none"
        >
          <ChevronRight
            size={11}
            class={`shrink-0 text-[var(--text-faint)] transition-transform duration-100 ${
              isOpen() ? "rotate-90" : ""
            }`}
          />
          <Show when={props.keyName !== undefined}>
            <span class="text-[var(--violet-text)]">{props.keyName}:</span>
          </Show>
          <span class="text-[var(--text-faint)]">
            {Array.isArray(props.value) ? "[" : "{"}
          </span>
          <Show when={!isOpen()}>
            <span class="text-[10.5px] text-[var(--text-muted)]">
              {Array.isArray(props.value)
                ? t("toolCard.jsonItemsCount", { count: totalCount() })
                : t("toolCard.jsonKeysCount", { count: totalCount() })}
            </span>
            <span class="text-[var(--text-faint)]">
              {Array.isArray(props.value) ? "]" : "}"}
            </span>
          </Show>
        </button>

        <Show when={isOpen()}>
          <div class="ml-3 border-l border-[var(--border-subtle)]/70 pl-2">
            <For each={entries()}>
              {([k, v]) => (
                <JsonNode
                  keyName={k}
                  value={v}
                  depth={props.depth + 1}
                  expandEpoch={props.expandEpoch}
                />
              )}
            </For>
            <Show when={totalCount() > MAX_VISIBLE_CHILDREN}>
              <div class="pl-4 text-[10.5px] italic text-[var(--text-faint)]">
                … +{totalCount() - MAX_VISIBLE_CHILDREN}
              </div>
            </Show>
          </div>
          <div class="pl-4 text-[var(--text-faint)]">
            {Array.isArray(props.value) ? "]" : "}"}
          </div>
        </Show>
      </Show>
    </div>
  );
}

export default function JsonTreeView(props: {
  data: Record<string, unknown> | unknown[];
  rawText: string;
  fullHeight?: boolean;
}) {
  const [mode, setMode] = createSignal<"tree" | "raw">("tree");
  const [expandEpoch, setExpandEpoch] = createSignal({ open: false, seq: 0 });
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const handleCopy = async () => {
    try {
      const formatted = JSON.stringify(props.data, null, 2);
      await navigator.clipboard.writeText(formatted);
      setCopied(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  };

  return (
    <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-code)]">
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)]/80 bg-[var(--bg-elevated)]/55 px-2.5 py-1 text-[10.5px]">
        <div class="inline-flex items-center rounded bg-[var(--bg-inset)] p-0.5">
          <button
            type="button"
            onClick={() => setMode("tree")}
            class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors cursor-pointer ${
              mode() === "tree"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <ListTree size={10.5} />
            <span>{t("toolCard.jsonTreeView")}</span>
          </button>
          <button
            type="button"
            onClick={() => setMode("raw")}
            class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors cursor-pointer ${
              mode() === "raw"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Code2 size={10.5} />
            <span>{t("toolCard.jsonRawView")}</span>
          </button>
        </div>

        <div class="flex items-center gap-1">
          <Show when={mode() === "tree"}>
            <button
              type="button"
              onClick={() => setExpandEpoch((prev) => ({ open: true, seq: prev.seq + 1 }))}
              class="rounded px-1.5 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("toolCard.jsonExpandAll")}
            </button>
            <button
              type="button"
              onClick={() => setExpandEpoch((prev) => ({ open: false, seq: prev.seq + 1 }))}
              class="rounded px-1.5 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              {t("toolCard.jsonCollapseAll")}
            </button>
          </Show>
          <button
            type="button"
            onClick={() => void handleCopy()}
            title={copied() ? t("common.copied") : t("toolCard.jsonCopyValue")}
            class="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <Show when={copied()} fallback={<Copy size={11} />}>
              <Check size={11} class="text-[var(--ok-text)]" />
            </Show>
          </button>
        </div>
      </div>

      <Show
        when={mode() === "tree"}
        fallback={
          <pre
            class={`overflow-auto p-2.5 font-mono text-[12px] text-[var(--text-secondary)] whitespace-pre-wrap break-all selectable-text ${
              props.fullHeight ? "" : "max-h-56"
            }`}
          >
            {props.rawText}
          </pre>
        }
      >
        <div class={`overflow-auto p-2.5 selectable-text ${props.fullHeight ? "" : "max-h-64"}`}>
          <JsonNode value={props.data} depth={0} expandEpoch={expandEpoch()} />
        </div>
      </Show>
    </div>
  );
}
