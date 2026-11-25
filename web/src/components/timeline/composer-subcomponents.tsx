// web/src/components/timeline/composer-subcomponents.tsx
import { Show } from "solid-js";
import {
  ArrowUp,
  Clock,
  FolderOpen,
  LoaderCircle,
  Square,
  Workflow,
} from "lucide-solid";
import { t } from "../../i18n";
import ModelSelector from "../models/ModelSelector";
import { ComposerSandboxPill } from "./ComposerSandboxPill";
import ContextProgressPill from "./ContextProgressPill";
import ApprovalModePill from "./ApprovalModePill";
import ModeSwitcher from "./ModeSwitcher";
import ComposerToolsPopover from "./ComposerToolsPopover";
import { ComposerImageOptionsBar, ComposerVoiceInputButton } from "./ComposerMediaControls";
import { isWorkSurface, surfaceExtension } from "../layout/surface-extension";

export {
  ComposerAttachmentChips,
  ComposerAutocompletePopover,
  ComposerEnhancePreview,
} from "./composer-attachment-chips";

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
            class="inline-flex h-7 shrink-0 items-center justify-center gap-1 whitespace-nowrap rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)]/70 px-2.5 text-[11px] font-medium leading-none text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <Show
              when={!props.spawningSubtask}
              fallback={<LoaderCircle size={12} class="shrink-0 animate-spin" />}
            >
              <Workflow size={12} class="shrink-0" />
            </Show>
            <span>{t("timeline.spawnSubtaskAside")}</span>
          </button>
        </Show>

        <Show when={props.canSend}>
          <button
            type="button"
            class="inline-flex h-7 shrink-0 items-center justify-center gap-1 whitespace-nowrap rounded-full bg-[var(--accent-primary)] px-2.5 text-[var(--accent-foreground)] shadow-xs hover:opacity-90 transition-all cursor-pointer"
            onClick={props.onSend}
            title={t("timeline.queueSend")}
          >
            <Show
              when={!props.sending}
              fallback={<LoaderCircle size={14} class="shrink-0 animate-spin" />}
            >
              <Clock size={12.5} strokeWidth={2.2} class="shrink-0" />
              <ArrowUp size={13.5} strokeWidth={2.5} class="shrink-0" />
            </Show>
          </button>
        </Show>

        <button
          type="button"
          class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] disabled:opacity-50 transition-colors cursor-pointer"
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
          class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
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
  onToggleEngine?: () => void;
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
    <>
      <ComposerImageOptionsBar sessionID={props.sessionID} />
      <div class="composer-toolbar mt-2 flex flex-col items-stretch justify-between gap-2">
        <div class="composer-toolbar-left flex min-w-0 items-center gap-1 flex-wrap">
          <ComposerToolsPopover
            workspaceReady={props.workspaceReady}
            skillActive={props.skillActive}
            runtime={props.runtime}
            busy={props.busy}
            canEnhance={props.canEnhance}
            enhancing={props.enhancing}
            onAttach={props.onAttach}
            onOpenSkills={props.onOpenSkills}
            onOpenCodeSearch={props.onOpenCodeSearch}
            onOpenConvoy={props.onOpenConvoy}
            onToggleEngine={props.onToggleEngine}
            onEnhance={props.onEnhance}
          />

          <ModeSwitcher />
          <ModelSelector openRequest={props.modelPickerRequest} />

          <Show when={!(isWorkSurface() && surfaceExtension.hideCodeComposerControls)}>
            <Show
              when={props.hasRootDir}
              fallback={
                <button
                  type="button"
                  onClick={props.onChooseFolder}
                  disabled={props.choosingFolder}
                  title={t("timeline.selectFolderHint")}
                  class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 py-1 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
                >
                  <FolderOpen size={14} class="shrink-0" />
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

        <div class="composer-toolbar-right flex flex-wrap items-center justify-end gap-1.5 self-end">
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
    </>
  );
}
