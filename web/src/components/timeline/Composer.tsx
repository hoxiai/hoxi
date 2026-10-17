// web/src/components/timeline/Composer.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowUp,
  GitFork,
  LoaderCircle,
  Slash,
  Square,
  WandSparkles,
} from "lucide-solid";
import { hoxiAPI, type CommandInfo } from "../../services/api";
import {
  clearReviewComments,
  forkFromMessage,
  isSessionBusy,
  newIsolatedSession,
  sessionDirectory,
  workspace,
} from "../../store/workspace";
import ModelSelector from "../models/ModelSelector";
import { FileIcon } from "../icons";
import ComposerShelf from "./ComposerShelf";
import ModeSwitcher from "./ModeSwitcher";

interface AutocompleteItem {
  type: "file" | "command";
  label: string;
  detail?: string;
  insertText: string;
}

export default function Composer() {
  const [text, setText] = createSignal("");
  const [sending, setSending] = createSignal(false);
  const [stopping, setStopping] = createSignal(false);
  const [forking, setForking] = createSignal(false);

  // Autocomplete (@file and /command) state
  const [suggestions, setSuggestions] = createSignal<AutocompleteItem[]>([]);
  const [selectedIndex, setSelectedIndex] = createSignal(0);
  const [triggerMode, setTriggerMode] = createSignal<"file" | "command" | null>(null);
  const [cachedCommands, setCachedCommands] = createSignal<CommandInfo[] | null>(null);

  let textareaRef: HTMLTextAreaElement | undefined;

  const sessionID = () => workspace.activeSessionID;
  const busy = createMemo(() => isSessionBusy(sessionID()));
  const mode = () => workspace.activeMode;

  const reviewComments = createMemo(() => {
    const id = sessionID();
    return id ? (workspace.reviewCommentsBySession[id] ?? []) : [];
  });

  const lastMessageID = createMemo(() => {
    const id = sessionID();
    if (!id) return undefined;
    const turns = workspace.messagesBySession[id];
    if (!turns || turns.length === 0) return undefined;
    return turns[turns.length - 1].info.id;
  });

  const canSend = () =>
    (text().trim().length > 0 || reviewComments().length > 0) && !sending();

  const adjustHeight = () => {
    if (!textareaRef) return;
    textareaRef.style.height = "auto";
    textareaRef.style.height = `${Math.min(180, Math.max(48, textareaRef.scrollHeight))}px`;
  };

  // Detect @file or /command trigger in text
  createEffect(() => {
    const val = text();
    const dir = sessionDirectory(sessionID());

    // 1. Check slash command at start: `/cmd`
    const cmdMatch = val.match(/^\/([^\s]*)$/);
    if (cmdMatch) {
      const query = cmdMatch[1].toLowerCase();
      setTriggerMode("command");
      void (async () => {
        let cmds = cachedCommands();
        if (!cmds) {
          try {
            cmds = await hoxiAPI.listCommands(dir);
            setCachedCommands(cmds);
          } catch {
            cmds = [];
          }
        }
        const filtered = (cmds ?? [])
          .filter(
            (c) =>
              c.name.toLowerCase().includes(query) ||
              (c.description ?? "").toLowerCase().includes(query)
          )
          .slice(0, 8)
          .map((c) => ({
            type: "command" as const,
            label: `/${c.name}`,
            detail: c.description || "Run command",
            insertText: `/${c.name} `,
          }));
        setSuggestions(filtered);
        setSelectedIndex(0);
      })();
      return;
    }

    // 2. Check @file mention at end of input
    const fileMatch = val.match(/(?:^|\s)@([^\s@]*)$/);
    if (fileMatch) {
      const query = fileMatch[1];
      setTriggerMode("file");
      const timer = setTimeout(async () => {
        try {
          let files: string[] = [];
          if (query.trim().length > 0) {
            files = await hoxiAPI.findFiles(query.trim(), dir);
          } else {
            const nodes = await hoxiAPI.listFiles("", dir);
            files = (nodes ?? []).map((n) => n.path);
          }
          setSuggestions(
            (files ?? []).slice(0, 8).map((f) => ({
              type: "file" as const,
              label: f,
              detail: "Workspace file",
              insertText: `@${f} `,
            }))
          );
          setSelectedIndex(0);
        } catch {
          setSuggestions([]);
        }
      }, 90);
      return () => clearTimeout(timer);
    }

    setTriggerMode(null);
    setSuggestions([]);
  });

  const applySuggestion = (item: AutocompleteItem) => {
    const val = text();
    if (item.type === "command") {
      setText(item.insertText);
    } else {
      const next = val.replace(/(?:^|\s)@([^\s@]*)$/, (m) => {
        const prefix = m.startsWith(" ") ? " " : "";
        return `${prefix}${item.insertText}`;
      });
      setText(next);
    }
    setTriggerMode(null);
    setSuggestions([]);
    textareaRef?.focus();
    adjustHeight();
  };

  const buildPayloadWithReviews = (): string => {
    const raw = text().trim();
    const comments = reviewComments();
    if (comments.length === 0) return raw;

    const reviewLines = comments.map((c) => {
      const loc = c.line ? `${c.file}:${c.line}` : c.file;
      return `- \`${loc}\`: ${c.body}`;
    });
    const header =
      "Please address the following inline code review comments:\n" +
      reviewLines.join("\n");
    return raw ? `${header}\n\n${raw}` : header;
  };

  const onSend = async () => {
    const payload = buildPayloadWithReviews();
    if (!payload || sending()) return;
    setSending(true);
    try {
      let id = sessionID();
      if (!id) {
        id = await newIsolatedSession(payload.slice(0, 48));
      }
      await hoxiAPI.sendPromptAsync(id, payload, {
        agent: mode(),
        model: workspace.selectedModel || undefined,
        variant: workspace.reasoningEffort || undefined,
        directory: sessionDirectory(id),
      });
      setText("");
      clearReviewComments(id);
      setTriggerMode(null);
      setSuggestions([]);
      if (textareaRef) {
        textareaRef.style.height = "48px";
      }
    } catch (err) {
      console.error("send prompt failed:", err);
    } finally {
      setSending(false);
    }
  };

  const onStop = async () => {
    const id = sessionID();
    if (!id || stopping()) return;
    setStopping(true);
    try {
      await hoxiAPI.abortSession(id, sessionDirectory(id));
    } catch (err) {
      console.error("abort failed:", err);
    } finally {
      setStopping(false);
    }
  };

  const onFork = async () => {
    const id = sessionID();
    if (!id || forking()) return;
    setForking(true);
    try {
      await forkFromMessage(id, lastMessageID());
    } catch (err) {
      console.error("fork failed:", err);
    } finally {
      setForking(false);
    }
  };

  const onKeyDown = (ev: KeyboardEvent) => {
    const list = suggestions();
    if (triggerMode() && list.length > 0) {
      if (ev.key === "ArrowDown") {
        ev.preventDefault();
        setSelectedIndex((i) => (i + 1) % list.length);
        return;
      }
      if (ev.key === "ArrowUp") {
        ev.preventDefault();
        setSelectedIndex((i) => (i - 1 + list.length) % list.length);
        return;
      }
      if (ev.key === "Tab" || (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing)) {
        ev.preventDefault();
        const chosen = list[selectedIndex()];
        if (chosen) applySuggestion(chosen);
        return;
      }
      if (ev.key === "Escape") {
        ev.preventDefault();
        setTriggerMode(null);
        setSuggestions([]);
        return;
      }
    }

    if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
      ev.preventDefault();
      void onSend();
    }
  };

  return (
    <div class="shrink-0 w-full px-6 pb-4 pt-1">
      <div class="relative mx-auto w-full max-w-[48rem]">
        {/* Floating @file / /command Autocomplete Popover */}
        <Show when={triggerMode() && suggestions().length > 0}>
          <div class="absolute bottom-full left-0 mb-2 w-full max-w-[28rem] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
            <div class="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              {triggerMode() === "command" ? "Slash Commands" : "Mention Workspace File"}
            </div>
            <div class="max-h-56 overflow-y-auto space-y-0.5">
              <For each={suggestions()}>
                {(item, idx) => (
                  <button
                    type="button"
                    class={`w-full flex items-center justify-between gap-3 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] transition-colors cursor-pointer ${
                      idx() === selectedIndex()
                        ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                        : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                    }`}
                    onMouseEnter={() => setSelectedIndex(idx())}
                    onClick={() => applySuggestion(item)}
                  >
                    <span class="inline-flex items-center gap-2 min-w-0 font-mono truncate">
                      {item.type === "command" ? (
                        <Slash size={12} class="text-[var(--accent-primary)] shrink-0" />
                      ) : (
                        <FileIcon
                          node={{
                            path: item.label,
                            type: item.label.endsWith("/") ? "directory" : "file",
                          }}
                          class="w-3.5 h-3.5 shrink-0"
                        />
                      )}
                      <span class="truncate">{item.label}</span>
                    </span>
                    <Show when={item.detail}>
                      <span class="text-[11px] text-[var(--text-muted)] truncate max-w-[160px]">
                        {item.detail}
                      </span>
                    </Show>
                  </button>
                )}
              </For>
            </div>
          </div>
        </Show>

        {/* Docked Approvals + Contextual Shelf (Reviews, Todos, Diffs) */}
        <ComposerShelf />

        {/* Main Glass Composer Box */}
        <div class="chat-composer-glass flex w-full flex-col rounded-2xl px-4 pt-3 pb-2.5">
          <textarea
            ref={textareaRef}
            class="w-full min-h-[48px] max-h-[180px] resize-none bg-transparent text-[14px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            rows={1}
            placeholder={
              busy()
                ? "Steer the agent mid-run…"
                : reviewComments().length > 0
                  ? "Add optional overall instructions, or press Enter to send review comments…"
                  : "Send a message, @ to mention files, or / for commands…"
            }
            value={text()}
            onInput={(ev) => {
              setText(ev.currentTarget.value);
              adjustHeight();
            }}
            onKeyDown={onKeyDown}
          />

          <div class="mt-2 flex items-center justify-between gap-2 flex-wrap">
            {/* Left Action Pills: Agent Mode, Model Selector, Fork */}
            <div class="flex items-center gap-1.5 flex-wrap">
              <ModeSwitcher />
              <ModelSelector />

              <Show when={sessionID()}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 transition-colors cursor-pointer"
                  disabled={forking()}
                  onClick={onFork}
                  title="Fork a new thread from the latest message"
                >
                  <Show
                    when={!forking()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <GitFork size={12} />
                  </Show>
                  Fork
                </button>
              </Show>
            </div>

            {/* Right Send / Stop / Steer Actions */}
            <div class="flex items-center gap-2">
              <Show when={busy()}>
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-full border border-[var(--violet-border)] bg-[var(--violet-surface)] px-3 py-1 text-[11.5px] font-medium text-[var(--violet-text)] disabled:opacity-40 transition-all cursor-pointer"
                  disabled={!canSend()}
                  onClick={onSend}
                  title="Send a live course-correction without aborting"
                >
                  <Show
                    when={!sending()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <WandSparkles size={12} />
                  </Show>
                  Steer
                </button>

                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--status-danger)] text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
                  disabled={stopping()}
                  onClick={onStop}
                  title="Stop generation"
                >
                  <Show
                    when={!stopping()}
                    fallback={<LoaderCircle size={13} class="animate-spin" />}
                  >
                    <Square size={11} class="fill-current" />
                  </Show>
                </button>
              </Show>

              <Show when={!busy()}>
                <button
                  type="button"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent-primary)] text-white shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
                  disabled={!canSend()}
                  onClick={onSend}
                  title="Send message (Enter)"
                >
                  <Show
                    when={!sending()}
                    fallback={<LoaderCircle size={14} class="animate-spin" />}
                  >
                    <ArrowUp size={15} strokeWidth={2.5} />
                  </Show>
                </button>
              </Show>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
