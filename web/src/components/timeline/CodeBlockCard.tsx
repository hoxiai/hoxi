import { createEffect, createMemo, createSignal, Index, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import { highlightCode, type HighlightedCode } from "./syntax-highlight";
import { workspace } from "../../store/workspace";
import { Icon } from "../icons";
import { ActionIconButton } from "./tool-card-helpers";

export default function CodeBlockCard(props: { lang: string; code: string }) {
  const [copied, setCopied] = createSignal(false);
  const [highlighted, setHighlighted] = createSignal<
    (HighlightedCode & { lang: string; theme: string }) | undefined
  >();
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let highlightTimer: ReturnType<typeof setTimeout> | undefined;
  let revision = 0;
  let disposed = false;
  let latest = { code: "", lang: "", theme: "dark" as "dark" | "light" };

  createEffect(() => {
    latest = {
      code: props.code,
      lang: props.lang,
      theme: workspace.theme === "dark" ? "dark" : "light",
    };
    revision++;
    // Throttle rather than debounce so continuous streaming still gets highlighted.
    if (highlightTimer !== undefined) return;
    highlightTimer = setTimeout(async () => {
      highlightTimer = undefined;
      const snapshot = latest;
      const request = revision;
      const result = await highlightCode(snapshot.code, snapshot.lang, snapshot.theme);
      if (!disposed && request === revision) {
        setHighlighted(result ? { ...result, lang: snapshot.lang, theme: snapshot.theme } : undefined);
      }
    }, 80);
  });

  onCleanup(() => {
    disposed = true;
    clearTimeout(highlightTimer);
    clearTimeout(copyTimer);
  });

  const visibleHighlight = createMemo(() => {
    const result = highlighted();
    if (!result || result.lang !== props.lang || result.theme !== workspace.theme ||
        !props.code.startsWith(result.code)) return;
    return result;
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

  return (
    <div class="my-1 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-code)]">
      <div class="flex items-center justify-between border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3.5 py-1.5 text-[12px] font-mono text-[var(--text-muted)]">
        <span class="inline-flex items-center gap-1.5">
          <Icon name="code-lines" size="small" class="text-[var(--text-faint)]" />
          {props.lang}
        </span>
        <ActionIconButton
          label={copied() ? t("common.copied") : t("common.copy")}
          tooltipSide="left"
          onClick={() => void onCopy()}
        >
          <Show when={copied()} fallback={<Icon name="copy" size="small" />}>
            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
          </Show>
        </ActionIconButton>
      </div>
      <pre
        class="overflow-x-auto p-4 font-mono leading-[1.65] text-[var(--text-primary)] selectable-text"
        style={{ "font-size": "var(--app-code-text-size)" }}
      >
        <code><Show when={visibleHighlight()} fallback={props.code}>{(result) => <>
          <Index each={result().tokens}>{(line, index) => <>
            {index > 0 ? "\n" : ""}
            <Index each={line()}>{(token) => <span style={{
              color: token().color,
              "font-style": (token().fontStyle ?? 0) & 1 ? "italic" : undefined,
              "font-weight": (token().fontStyle ?? 0) & 2 ? "bold" : undefined,
              "text-decoration": (token().fontStyle ?? 0) & 4 ? "underline" : undefined,
            }}>{token().content}</span>}</Index>
          </>}</Index>
          {props.code.slice(result().code.length)}
        </>}</Show></code>
      </pre>
    </div>
  );
}
