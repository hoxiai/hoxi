import { createEffect, createMemo, createSignal, Index, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import {
  getCachedHighlight,
  highlightCode,
  highlightCodeSync,
  type HighlightedCode,
  stabilizeHighlightedLines,
} from "./syntax-highlight";
import { codeWrapEnabled, toggleCodeWrap, workspace } from "../../store/workspace";
import { Icon } from "../icons";
import { ActionIconButton } from "./tool-card-helpers";

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

export default function CodeBlockCard(props: {
  lang: string;
  code: string;
  ref?: (el: HTMLDivElement) => void;
}) {
  const initialTheme = workspace.theme === "dark" ? "dark" : "light";
  const initialCached = getCachedHighlight(props.code, props.lang, initialTheme);
  const [copied, setCopied] = createSignal(false);
  const wrap = codeWrapEnabled;
  let lastState: (HighlightedCode & { lang: string; theme: string }) | undefined = initialCached
    ? { ...initialCached, lang: props.lang, theme: initialTheme }
    : undefined;
  const [highlighted, setHighlighted] = createSignal<
    (HighlightedCode & { lang: string; theme: string }) | undefined
  >(lastState);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let highlightTimer: ReturnType<typeof setTimeout> | undefined;
  let revision = 0;
  let disposed = false;
  let lastCachedCode = initialCached?.code ?? "";
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
    const asyncRes = highlighted();
    if (
      asyncRes &&
      asyncRes.code === props.code &&
      asyncRes.lang === props.lang &&
      asyncRes.theme === theme
    ) {
      lastState = asyncRes;
      lastCachedCode = asyncRes.code;
      return asyncRes;
    }
    const sync = highlightCodeSync(
      props.code,
      props.lang,
      theme,
      lastCachedCode || undefined
    );
    if (sync) {
      lastCachedCode = sync.code;
      const tokens =
        lastState && lastState.lang === props.lang && lastState.theme === theme
          ? stabilizeHighlightedLines(lastState.tokens, sync.tokens)
          : sync.tokens;
      lastState = { code: sync.code, tokens, lang: props.lang, theme };
      return lastState;
    }
    if (
      lastState &&
      lastState.lang === props.lang &&
      lastState.theme === theme &&
      props.code.startsWith(lastState.code)
    ) {
      return lastState;
    }
    return undefined;
  });

  createEffect(() => {
    const theme = workspace.theme === "dark" ? "dark" : "light";
    latest = {
      code: props.code,
      lang: props.lang,
      theme,
    };
    revision++;
    const current = visibleHighlight();
    if (current && current.code === latest.code) {
      if (highlightTimer !== undefined) {
        clearTimeout(highlightTimer);
        highlightTimer = undefined;
      }
      return;
    }
    // Throttle rather than debounce so continuous streaming still gets highlighted.
    if (highlightTimer !== undefined) return;
    highlightTimer = setTimeout(async () => {
      highlightTimer = undefined;
      const snapshot = latest;
      const request = revision;
      const result = await highlightCode(
        snapshot.code,
        snapshot.lang,
        snapshot.theme,
        lastCachedCode || undefined
      );
      if (result) lastCachedCode = result.code;
      if (!disposed) {
        if (request === revision) {
          applyHighlight(result, snapshot.lang, snapshot.theme);
        } else {
          const caughtUp = highlightCodeSync(
            latest.code,
            latest.lang,
            latest.theme,
            lastCachedCode || undefined
          );
          if (caughtUp) {
            lastCachedCode = caughtUp.code;
            applyHighlight(caughtUp, latest.lang, latest.theme);
          }
        }
      }
    }, 80);
  });

  onCleanup(() => {
    disposed = true;
    clearTimeout(highlightTimer);
    clearTimeout(copyTimer);
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

  const toggleWrap = toggleCodeWrap;

  const codeKind = () => detectCodeKind(props.lang);

  return (
    <div
      ref={props.ref}
      class={`group/code relative my-1 overflow-hidden rounded-xl border bg-[var(--bg-code)] ${
        codeKind() === "diff"
          ? "border-[var(--violet-border)]/60"
          : "border-[var(--border-subtle)]"
      }`}
    >
      <div class="pointer-events-none absolute right-2 top-2 z-10 flex items-center justify-end gap-1 opacity-0 transition-opacity duration-150 group-hover/code:pointer-events-auto group-hover/code:opacity-100 group-focus-within/code:pointer-events-auto group-focus-within/code:opacity-100">
        <span
          class={`inline-flex items-center gap-1 rounded-md bg-[var(--bg-elevated)]/90 px-1.5 py-0.5 font-mono text-[11px] shadow-sm ${
            codeKind() === "shell"
              ? "text-[var(--ok-text)]"
              : codeKind() === "diff"
                ? "text-[var(--violet-text)]"
                : "text-[var(--text-muted)]"
          }`}
        >
          <Icon name="code-lines" size="small" />
          {props.lang}
        </span>
        <span class="inline-flex items-center gap-0.5 rounded-md bg-[var(--bg-elevated)]/90 p-0.5">
          <ActionIconButton
            label={wrap() ? t("timeline.codeWrapOff") : t("timeline.codeWrapOn")}
            tooltipSide="left"
            onClick={toggleWrap}
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
      <pre
        data-scrollable
        class={`overflow-x-auto p-4 font-mono leading-[1.65] text-[var(--text-primary)] selectable-text ${
          wrap() ? "whitespace-pre-wrap break-words" : "whitespace-pre"
        }`}
        style={{ "font-size": "var(--app-code-text-size)" }}
      >
        <code><Show when={visibleHighlight()} fallback={props.code || "\u00A0"}>{(result) => <>
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
