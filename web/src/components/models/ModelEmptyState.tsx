// web/src/components/models/ModelEmptyState.tsx
import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  KeyRound,
  LoaderCircle,
  RefreshCw,
  Sparkles,
  Terminal,
} from "lucide-solid";
import type { LocalCLIStatusItem } from "../../services/api";
import {
  clearFreeOpenCodeSetupError,
  desktopAuth,
  enableFreeOpenCodeModels,
  ensureLocalCLIStatusLoaded,
  errorMessageOf,
  freeOpenCodeSetupState,
  isRunnerAuthMode,
  localCLIStatusItems,
  openProviderModal,
  openSettingsModal,
  reloadRuntimeCatalogs,
  syncLocalCLIModel,
} from "../../store/workspace";
import { t } from "../../i18n";

export function ModelEmptyState(props: {
  mode: "empty" | "free-empty";
  onClosePopover: () => void;
}) {
  const [importingID, setImportingID] = createSignal<string | null>(null);
  const [rescanning, setRescanning] = createSignal(false);
  const [importError, setImportError] = createSignal<string | null>(null);
  let mounted = true;
  onCleanup(() => {
    mounted = false;
  });

  onMount(() => {
    if (props.mode === "empty") {
      void ensureLocalCLIStatusLoaded(true);
    }
  });

  const importableTools = createMemo<LocalCLIStatusItem[]>(() => {
    if (props.mode !== "empty") return [];
    return localCLIStatusItems().filter(
      (item) =>
        item.installed &&
        item.authenticated &&
        !item.connected &&
        !isRunnerAuthMode(item.authMode)
    );
  });

  const isFreeBusy = createMemo(
    () =>
      freeOpenCodeSetupState.phase === "checking" ||
      freeOpenCodeSetupState.phase === "installing" ||
      freeOpenCodeSetupState.phase === "loading"
  );

  const freeBusyLabel = createMemo(() => {
    switch (freeOpenCodeSetupState.phase) {
      case "checking":
        return t("models.freeSetupChecking");
      case "installing":
        return t("models.freeSetupInstalling");
      case "loading":
        return t("models.freeSetupLoading");
      default:
        return t("models.freeSetupActionBtn");
    }
  });

  const errorBanner = createMemo(() => importError() || freeOpenCodeSetupState.error);

  const handleEnableFree = async () => {
    if (isFreeBusy() || importingID()) return;
    setImportError(null);
    clearFreeOpenCodeSetupError();
    try {
      await enableFreeOpenCodeModels();
      if (mounted) props.onClosePopover();
    } catch {
      /* store error state already surfaces the message */
    }
  };

  const handleImportTool = async (tool: LocalCLIStatusItem) => {
    if (isFreeBusy() || importingID()) return;
    setImportingID(tool.id);
    setImportError(null);
    try {
      await syncLocalCLIModel(tool);
      if (mounted) props.onClosePopover();
    } catch (err) {
      if (mounted) {
        setImportError(errorMessageOf(err, t("common.error")));
      }
    } finally {
      if (mounted) setImportingID(null);
    }
  };

  const handleRescan = async () => {
    if (rescanning() || isFreeBusy() || importingID()) return;
    setRescanning(true);
    setImportError(null);
    try {
      await reloadRuntimeCatalogs();
    } catch (err) {
      if (mounted) {
        setImportError(errorMessageOf(err, t("common.error")));
      }
    } finally {
      if (mounted) setRescanning(false);
    }
  };

  return (
    <div class="flex min-h-full flex-col justify-center gap-3 p-3.5">
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 p-3.5 space-y-3">
        <div class="flex items-start gap-2.5">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--ok-surface)] text-[var(--ok-text)]">
            <Sparkles size={15} />
          </div>
          <div class="min-w-0 space-y-0.5">
            <div class="text-[13px] font-semibold text-[var(--text-primary)]">
              {props.mode === "empty"
                ? t("models.freeSetupEmptyTitle")
                : t("models.freeSetupFreeRailTitle")}
            </div>
            <p class="text-[11.5px] leading-relaxed text-[var(--text-muted)]">
              {props.mode === "empty"
                ? t("models.freeSetupEmptyDesc")
                : t("models.freeSetupFreeRailDesc")}
            </p>
          </div>
        </div>

        <div class="space-y-1.5">
          <button
            type="button"
            disabled={isFreeBusy() || Boolean(importingID())}
            onClick={() => void handleEnableFree()}
            class="hoxi-press w-full inline-flex items-center justify-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-2 text-[12.5px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-60 transition-opacity cursor-pointer"
          >
            <Show
              when={!isFreeBusy()}
              fallback={<LoaderCircle size={13} class="animate-spin shrink-0" />}
            >
              <Sparkles size={13} class="shrink-0" />
            </Show>
            <span class="truncate">{freeBusyLabel()}</span>
          </button>
          <div class="text-center text-[11px] text-[var(--text-faint)]">
            {t("models.freeSetupActionSub")}
          </div>
        </div>

        <Show when={errorBanner()}>
          {(msg) => (
            <div class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)]/50 px-2.5 py-1.5 text-[11px] text-[var(--danger-text)] break-words">
              {msg()}
            </div>
          )}
        </Show>
      </div>

      <Show when={importableTools().length > 0}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 p-2.5 space-y-2">
          <div class="flex items-center gap-1.5 px-0.5 text-[11px] font-semibold text-[var(--text-secondary)]">
            <Terminal size={12} class="text-[var(--accent-primary)] shrink-0" />
            <span>{t("models.freeSetupDetectedToolsTitle")}</span>
          </div>
          <div class="space-y-1.5">
            <For each={importableTools()}>
              {(tool) => (
                <div class="flex items-center justify-between gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5">
                  <span class="truncate text-[12px] font-medium text-[var(--text-primary)]">
                    {tool.name}
                  </span>
                  <button
                    type="button"
                    disabled={isFreeBusy() || Boolean(importingID())}
                    onClick={() => void handleImportTool(tool)}
                    class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
                  >
                    <Show when={importingID() === tool.id}>
                      <LoaderCircle size={10} class="animate-spin" />
                    </Show>
                    {t("models.freeSetupImportToolBtn")}
                  </button>
                </div>
              )}
            </For>
          </div>
        </div>
      </Show>

      <div class="flex flex-wrap items-center justify-center gap-1.5">
        <button
          type="button"
          onClick={() => {
            props.onClosePopover();
            openProviderModal("providers", null);
          }}
          class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
        >
          <KeyRound size={11} />
          {t("models.connectProviderOrAdd")}
        </button>

        <Show when={props.mode === "empty" && !desktopAuth.isLoggedIn}>
          <button
            type="button"
            onClick={() => {
              props.onClosePopover();
              openSettingsModal("official");
            }}
            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <Sparkles size={11} />
            {t("models.freeSetupOfficialBtn")}
          </button>
        </Show>

        <Show when={props.mode === "empty"}>
          <button
            type="button"
            disabled={rescanning() || isFreeBusy()}
            onClick={() => void handleRescan()}
            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 cursor-pointer"
            title={t("models.freeSetupRescanBtn")}
          >
            <RefreshCw size={11} class={rescanning() ? "animate-spin" : ""} />
            {t("models.freeSetupRescanBtn")}
          </button>
        </Show>
      </div>
    </div>
  );
}
