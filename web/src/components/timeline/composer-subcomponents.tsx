// web/src/components/timeline/composer-subcomponents.tsx
import { createEffect, For, Show } from "solid-js";
import {
  ArrowUp,
  AtSign,
  Clock,
  FolderOpen,
  LoaderCircle,
  Sparkles,
  Square,
  Workflow,
  X,
} from "lucide-solid";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import ModelSelector from "../models/ModelSelector";
import { ComposerSandboxPill } from "./ComposerSandboxPill";
import ContextProgressPill from "./ContextProgressPill";
import ApprovalModePill from "./ApprovalModePill";
import ModeSwitcher from "./ModeSwitcher";
import ComposerToolsPopover from "./ComposerToolsPopover";
import { ComposerImageOptionsBar, ComposerVoiceInputButton } from "./ComposerMediaControls";
import { isWorkSurface, surfaceExtension } from "../layout/surface-extension";
import type { AutocompleteItem } from "./composer-hooks";

export { ComposerAttachmentChips, ComposerEnhancePreview } from "./composer-attachment-chips";

export function ComposerAutocompletePopover(props: {
  triggerMode: "file" | "command" | null;
  suggestions: AutocompleteItem[];
  selectedIndex: number;
  onSelectIndex: (idx: number) => void;
  onApply: (item: AutocompleteItem) => void;
}) {
  let listRef: HTMLDivElement | undefined;

  createEffect(() => {
    const idx = props.selectedIndex;
    if (!props.triggerMode || props.suggestions.length === 0) return;
    queueMicrotask(() => {
      const btn = listRef?.querySelector<HTMLButtonElement>(
        `[data-ac-index="${idx}"]`
      );
      btn?.scrollIntoView({ block: "nearest" });
    });
  });

  return (
    <Show when={props.triggerMode && props.suggestions.length > 0}>
      <div class="absolute bottom-full left-0 mb-2 w-full max-w-[32rem] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
        <div class="px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
          {props.triggerMode === "command"
            ? t("timeline.slashCommandsHeader")
            : t("timeline.mentionFileOrContextHeader")}
        </div>
        <div ref={listRef} class="max-h-56 overflow-y-auto space-y-0.5">
          <For each={props.suggestions}>
            {(item, idx) => (
              <button
                type="button"
                data-ac-index={idx()}
                class={`w-full flex flex-col gap-0.5 rounded-lg px-2.5 py-1 text-left text-[13px] transition-colors cursor-pointer ${
                  idx() === props.selectedIndex
                    ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                    : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
                onMouseEnter={() => props.onSelectIndex(idx())}
                onClick={() => props.onApply(item)}
              >
                <span class="flex w-full items-center gap-2 min-w-0 font-mono">
                  {item.source === "skill" ? (
                    <Sparkles size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : item.type === "context" ? (
                    <AtSign size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : item.type === "file" ? (
                    <FileIcon
                      node={{
                        path: item.label,
                        type: item.label.endsWith("/") ? "directory" : "file",
                      }}
                      class="w-3.5 h-3.5 shrink-0"
                    />
                  ) : null}
                  <span class="truncate">
                    {item.type === "context" ? item.label.replace(/^@/, "") : item.label}
                  </span>
                  <Show when={item.hints && item.hints.length > 0}>
                    <span class="shrink-0 font-sans text-[11px] text-[var(--text-faint)]">
                      {item.hints!.join(" ")}
                    </span>
                  </Show>
                  <Show when={item.source}>
                    <span class="ml-auto shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-sans text-[10px] font-medium uppercase tracking-wider text-[var(--text-faint)]">
                      {item.source === "skill"
                        ? t("skills.groupSkills")
                        : item.source === "mcp"
                        ? t("skills.groupMcp")
                        : t("skills.groupCommands")}
                    </span>
                  </Show>
                </span>
                <Show when={item.detail}>
                  <span class="w-full truncate font-sans text-[11px] leading-tight text-[var(--text-muted)]">
                    {item.detail}
                  </span>
                </Show>
              </button>
            )}
          </For>
        </div>
      </div>
    </Show>
  );
}

export function ComposerSendButtons(props: {
  busy: boolean;
  canSend: boolean;
  canSpawnSubtask: boolean;
  sending: boolean;
  spawningSubtask: boolean;
  stopping: boolean;
  sendShortcut: string;
  onSend: () => void;
  onSpawnSubtask: () => void;
  onStop: () => void;
}) {
  return (
    <>
      <Show when={props.busy}>
        <Show when={props.canSpawnSubtask}>
          <button
            type="button"
            disabled={props.spawningSubtask || props.sending}
            onClick={props.onSpawnSubtask}
            title={t("timeline.spawnSubtaskAsideTooltip")}
            class="inline-flex h-7 items-center justify-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)]/70 px-2.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <Show
              when={!props.spawningSubtask}
              fallback={<LoaderCircle size={12} class="animate-spin" />}
            >
              <Workflow size={12} />
            </Show>
            <span>{t("timeline.spawnSubtaskAside")}</span>
          </button>
        </Show>

        <Show when={props.canSend}>
          <button
            type="button"
            class="inline-flex h-7 items-center justify-center gap-1 rounded-full bg-[var(--accent-primary)] px-2.5 text-[var(--accent-foreground)] shadow-xs hover:opacity-90 transition-all cursor-pointer"
            onClick={props.onSend}
            title={t("timeline.queueSend")}
          >
            <Show
              when={!props.sending}
              fallback={<LoaderCircle size={14} class="animate-spin" />}
            >
              <Clock size={12.5} strokeWidth={2.2} />
              <ArrowUp size={13.5} strokeWidth={2.5} />
            </Show>
          </button>
        </Show>

        <button
          type="button"
          class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] disabled:opacity-50 transition-colors cursor-pointer"
          disabled={props.stopping}
          onClick={props.onStop}
          title={t("timeline.stopTooltip")}
        >
          <Show
            when={!props.stopping}
            fallback={<LoaderCircle size={13} class="animate-spin" />}
          >
            <Square size={10} class="fill-current" />
          </Show>
        </button>
      </Show>

      <Show when={!props.busy}>
        <button
          type="button"
          class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
          disabled={!props.canSend}
          onClick={props.onSend}
          title={`${t("timeline.send")} (${
            props.sendShortcut === "mod-enter" ? "⌘/Ctrl+Enter" : "Enter"
          })`}
        >
          <Show
            when={!props.sending}
            fallback={<LoaderCircle size={14} class="animate-spin" />}
          >
            <ArrowUp size={15} strokeWidth={2.5} />
          </Show>
        </button>
      </Show>
    </>
  );
}

export function ComposerBottomToolbar(props: {
  workspaceReady: boolean;
  hasRootDir: boolean;
  skillActive: boolean;
  sandboxMode: "local" | "worktree";
  runtime: string;
  canEnhance: boolean;
  enhancing: boolean;
  choosingFolder: boolean;
  switchingSandbox: boolean;
  busy: boolean;
  sessionID: string | null;
  directory: string;
  forking: boolean;
  modelPickerRequest: number;
  canSend: boolean;
  canSpawnSubtask: boolean;
  sending: boolean;
  spawningSubtask: boolean;
  stopping: boolean;
  sendShortcut: "enter" | "mod-enter";
  onAttach: () => void;
  onOpenSkills: () => void;
  onOpenCodeSearch: () => void;
  onOpenConvoy: () => void;
  onSwitchSandbox: (mode: "local" | "worktree") => void;
  onEnhance: () => void;
  onChooseFolder: () => void;
  onFork: () => void;
  onSend: () => void;
  onSpawnSubtask: () => void;
  onStop: () => void;
  onInsertDictation?: (text: string) => void;
  onMediaNotice?: (message: string) => void;
}) {
  return (
    <div class="composer-toolbar mt-2 flex flex-col items-stretch justify-between gap-2">
      <div class="flex items-center gap-1 flex-wrap">
        <ComposerToolsPopover
          workspaceReady={props.workspaceReady}
          skillActive={props.skillActive}
          sandboxMode={props.sandboxMode}
          runtime={props.runtime}
          canEnhance={props.canEnhance}
          enhancing={props.enhancing}
          onAttach={props.onAttach}
          onOpenSkills={props.onOpenSkills}
          onOpenCodeSearch={props.onOpenCodeSearch}
          onOpenConvoy={props.onOpenConvoy}
          onSwitchSandbox={() => props.onSwitchSandbox("worktree")}
          onEnhance={props.onEnhance}
        />

        <ModeSwitcher />
        <ModelSelector openRequest={props.modelPickerRequest} />
        <ComposerImageOptionsBar sessionID={props.sessionID} />

        <Show when={!(isWorkSurface() && surfaceExtension.hideCodeComposerControls)}>
          <Show
            when={props.hasRootDir}
            fallback={
              <button
                type="button"
                onClick={props.onChooseFolder}
                disabled={props.choosingFolder}
                title={t("timeline.selectFolderHint")}
                class="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
              >
                <FolderOpen size={14} />
                {t("timeline.selectFolder")}
              </button>
            }
          >
            <ComposerSandboxPill
              mode={props.sandboxMode}
              switching={props.switchingSandbox}
              busy={props.busy}
              sessionID={props.sessionID}
              directory={props.directory}
              forking={props.forking}
              onToggle={(m) => props.onSwitchSandbox(m)}
              onFork={props.onFork}
            />
          </Show>
        </Show>
      </div>

      <div class="flex items-center gap-1.5 self-end">
        <ContextProgressPill />
        <ApprovalModePill runtime={props.runtime} sandboxMode={props.sandboxMode} />
        <ComposerVoiceInputButton
          disabled={props.sending}
          onInsertText={props.onInsertDictation}
          onNotice={props.onMediaNotice}
        />

        <ComposerSendButtons
          busy={props.busy}
          canSend={props.canSend}
          canSpawnSubtask={props.canSpawnSubtask}
          sending={props.sending}
          spawningSubtask={props.spawningSubtask}
          stopping={props.stopping}
          sendShortcut={props.sendShortcut}
          onSend={props.onSend}
          onSpawnSubtask={props.onSpawnSubtask}
          onStop={props.onStop}
        />
      </div>
    </div>
  );
}
