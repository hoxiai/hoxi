// web/src/components/tasks/IntakeLinksModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Check, ClipboardCheck, Copy, Link2, Plus, Sparkles, Trash2, X } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { surfaceExtension } from "../layout/surface-extension";
import { t } from "../../i18n";
import {
  createIntakeLink,
  formatIntakeError,
  intakeLinks,
  loadIntakeLinks,
  openSettingsModal,
  removeIntakeLink,
  type IntakeLinkItem,
} from "../../store/workspace";

function CopyUrlRow(props: { badge: string; url: string }) {
  const [copied, setCopied] = createSignal(false);
  let timer: ReturnType<typeof setTimeout> | null = null;

  onCleanup(() => {
    if (timer !== null) clearTimeout(timer);
  });

  const onCopy = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      void navigator.clipboard.writeText(props.url).then(() => {
        setCopied(true);
        if (timer !== null) clearTimeout(timer);
        timer = setTimeout(() => setCopied(false), 1800);
      });
    }
  };

  return (
    <div class="flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5">
      <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-secondary)]">
        {props.badge}
      </span>
      <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-[var(--text-primary)]">
        {props.url}
      </span>
      <button
        type="button"
        onClick={onCopy}
        class="inline-flex shrink-0 items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
      >
        <Show when={copied()} fallback={<Copy size={11} />}>
          <Check size={11} class="text-[var(--ok-text)]" />
        </Show>
        <span>{copied() ? t("tasks.intakeCopiedBtn") : t("tasks.intakeCopyBtn")}</span>
      </button>
    </div>
  );
}

export default function IntakeLinksModal(props: {
  open: boolean;
  directory: string;
  onClose: () => void;
}) {
  const extraTabs = createMemo(() =>
    surfaceExtension.enabled ? (surfaceExtension.collaborationLinkTabs?.() ?? []) : []
  );
  const [activeTab, setActiveTab] = createSignal<string>(extraTabs()[0]?.id ?? "qa");
  const [label, setLabel] = createSignal("");
  const [creating, setCreating] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const selectedExtraTab = createMemo(
    () => extraTabs().find((tab) => tab.id === activeTab()) ?? null
  );

  createEffect(() => {
    const dir = props.directory.trim();
    if (!props.open || !dir) {
      setError(null);
      return;
    }
    if (activeTab() !== "qa" && !extraTabs().some((tab) => tab.id === activeTab())) {
      setActiveTab(extraTabs()[0]?.id ?? "qa");
    }
    for (const tab of extraTabs()) {
      tab.onOpen?.(dir);
    }
    void loadIntakeLinks(dir).catch((err) => {
      setError(formatIntakeError(err, "Failed to load links"));
    });
  });

  const onCreate = async (ev: Event) => {
    ev.preventDefault();
    const dir = props.directory.trim();
    if (!dir || creating()) return;
    setCreating(true);
    setError(null);
    try {
      await createIntakeLink({
        directory: dir,
        label: label().trim() || t("tasks.intakeLinksBtn"),
      });
      setLabel("");
    } catch (err) {
      setError(formatIntakeError(err, "Failed to create link"));
    } finally {
      setCreating(false);
    }
  };

  const onDelete = async (item: IntakeLinkItem) => {
    setError(null);
    try {
      await removeIntakeLink(item.id, props.directory);
    } catch (err) {
      setError(formatIntakeError(err, "Failed to delete link"));
    }
  };

  const hasAnyRelay = () => intakeLinks().some((item) => Boolean(item.relayUrl));

  const modalTitle = () =>
    extraTabs().length > 0
      ? (surfaceExtension.collaborationLinksButtonLabel?.() ?? t("tasks.intakeLinksModalTitle"))
      : t("tasks.intakeLinksModalTitle");

  const modalSubtitle = () =>
    selectedExtraTab()?.subtitle() ?? t("tasks.intakeLinksModalSubtitle");

  return (
    <Presence when={props.open}>
      {(state) => (
        <div
          data-state={state()}
          class="hoxi-modal-backdrop fixed inset-0 z-[80] flex items-center justify-center p-4"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) props.onClose();
          }}
        >
          <div class="hoxi-modal-panel hoxi-elevation-4 flex max-h-[85vh] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)]">
            <div class="flex items-start justify-between gap-3 border-b border-[var(--border-subtle)] px-5 py-4">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <Link2 size={16} class="text-[var(--accent-primary)]" />
                  <h2 class="text-[14px] font-semibold text-[var(--text-primary)]">
                    {modalTitle()}
                  </h2>
                </div>
                <p class="mt-1 text-[12px] leading-relaxed text-[var(--text-muted)]">
                  {modalSubtitle()}
                </p>
              </div>
              <button
                type="button"
                onClick={() => props.onClose()}
                class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X size={16} />
              </button>
            </div>

            <Show when={extraTabs().length > 0}>
              <div class="flex items-center gap-1.5 border-b border-[var(--border-subtle)]/70 bg-[var(--bg-panel)]/50 px-5 py-2.5">
                <For each={extraTabs()}>
                  {(extTab) => (
                    <button
                      type="button"
                      onClick={() => {
                        setActiveTab(extTab.id);
                        setError(null);
                      }}
                      class={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12px] font-medium transition-colors cursor-pointer ${
                        activeTab() === extTab.id
                          ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] ring-1 ring-[var(--accent-border)]"
                          : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                      }`}
                    >
                      <Sparkles size={13} />
                      <span>{extTab.label()}</span>
                      <Show when={(extTab.badgeCount?.() ?? 0) > 0}>
                        <span class="rounded bg-[var(--bg-panel)] px-1 font-mono text-[10px]">
                          {extTab.badgeCount?.()}
                        </span>
                      </Show>
                    </button>
                  )}
                </For>
                <button
                  type="button"
                  onClick={() => {
                    setActiveTab("qa");
                    setError(null);
                  }}
                  class={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-[12px] font-medium transition-colors cursor-pointer ${
                    activeTab() === "qa"
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] ring-1 ring-[var(--accent-border)]"
                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <ClipboardCheck size={13} />
                  <span>{t("tasks.intakeTabQA")}</span>
                  <Show when={intakeLinks().length > 0}>
                    <span class="rounded bg-[var(--bg-panel)] px-1 font-mono text-[10px]">
                      {intakeLinks().length}
                    </span>
                  </Show>
                </button>
              </div>
            </Show>

            <div class="min-h-0 flex-1 space-y-3 overflow-y-auto px-5 py-4">
              <Show
                when={selectedExtraTab()}
                fallback={
                  <>
                    <form onSubmit={(e) => void onCreate(e)} class="flex items-center gap-2">
                      <input
                        type="text"
                        value={label()}
                        onInput={(e) => setLabel(e.currentTarget.value)}
                        placeholder={t("tasks.intakeLabelPlaceholder")}
                        class="min-w-0 flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                      />
                      <button
                        type="submit"
                        disabled={creating() || !props.directory.trim()}
                        class="inline-flex shrink-0 items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
                      >
                        <Plus size={13} />
                        <span>
                          {creating() ? t("tasks.intakeCreatingBtn") : t("tasks.intakeCreateBtn")}
                        </span>
                      </button>
                    </form>

                    <Show when={error()}>
                      <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-2 text-[12px] text-[var(--danger-text)]">
                        {error()}
                      </div>
                    </Show>

                    <Show
                      when={intakeLinks().length > 0}
                      fallback={
                        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-6 text-center text-[12px] text-[var(--text-muted)]">
                          {t("tasks.intakeLinksEmpty")}
                        </div>
                      }
                    >
                      <div class="space-y-2.5">
                        <For each={intakeLinks()}>
                          {(item) => (
                            <div class="space-y-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3">
                              <div class="flex items-center justify-between gap-2">
                                <div class="flex min-w-0 items-center gap-2">
                                  <span class="truncate text-[12px] font-semibold text-[var(--text-primary)]">
                                    {item.label}
                                  </span>
                                  <span class="rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                                    {item.projectName}
                                  </span>
                                </div>
                                <button
                                  type="button"
                                  onClick={() => void onDelete(item)}
                                  class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                                >
                                  <Trash2 size={13} />
                                </button>
                              </div>

                              <div class="space-y-1.5">
                                <Show when={item.relayUrl}>
                                  <CopyUrlRow
                                    badge={t("tasks.intakeUrlRelay")}
                                    url={item.relayUrl!}
                                  />
                                </Show>
                                <Show when={item.lanUrl}>
                                  <CopyUrlRow badge={t("tasks.intakeUrlLan")} url={item.lanUrl!} />
                                </Show>
                                <Show when={item.localUrl || item.portalPath}>
                                  <CopyUrlRow
                                    badge={t("tasks.intakeUrlLocal")}
                                    url={item.localUrl || item.portalPath}
                                  />
                                </Show>
                              </div>
                            </div>
                          )}
                        </For>
                      </div>
                    </Show>

                    <Show when={intakeLinks().length > 0 && !hasAnyRelay()}>
                      <div class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-2 text-[11px] text-[var(--text-muted)]">
                        <span>{t("tasks.intakeRelayHint")}</span>
                        <button
                          type="button"
                          onClick={() => {
                            props.onClose();
                            openSettingsModal("remote");
                          }}
                          class="shrink-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          {t("tasks.intakeRelaySettingsBtn")}
                        </button>
                      </div>
                    </Show>
                  </>
                }
              >
                {(extTab) =>
                  extTab().render({ directory: props.directory, onClose: props.onClose })
                }
              </Show>
            </div>
          </div>
        </div>
      )}
    </Presence>
  );
}
