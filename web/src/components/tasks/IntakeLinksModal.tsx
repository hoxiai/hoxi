// web/src/components/tasks/IntakeLinksModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  ClipboardCheck,
  Copy,
  FolderLock,
  Link2,
  Lock,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
  X,
} from "lucide-solid";
import { Presence } from "../motion/Presence";
import { surfaceExtension } from "../layout/surface-extension";
import { t } from "../../i18n";
import {
  createIntakeLink,
  formatIntakeError,
  intakeLinks,
  loadIntakeLinks,
  openSettingsModal,
  parseScopedPathInput,
  removeIntakeLink,
  updateIntakeLink,
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

type PathScopeMode = "readonly" | "writable";

function PathScopeEditor(props: {
  mode: PathScopeMode;
  onModeChange: (mode: PathScopeMode) => void;
  readOnlyText: string;
  onReadOnlyChange: (value: string) => void;
  writableText: string;
  onWritableChange: (value: string) => void;
}) {
  return (
    <div class="space-y-2">
      <div class="flex flex-wrap items-center gap-1.5">
        <button
          type="button"
          onClick={() => props.onModeChange("readonly")}
          class={`inline-flex items-center gap-1 rounded-lg border px-2.5 py-1 text-[11px] font-medium transition-colors cursor-pointer ${
            props.mode === "readonly"
              ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          }`}
        >
          <Lock size={11} />
          <span>{t("tasks.intakeScopeModeReadOnly")}</span>
        </button>
        <button
          type="button"
          onClick={() => props.onModeChange("writable")}
          class={`inline-flex items-center gap-1 rounded-lg border px-2.5 py-1 text-[11px] font-medium transition-colors cursor-pointer ${
            props.mode === "writable"
              ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          }`}
        >
          <FolderLock size={11} />
          <span>{t("tasks.intakeScopeModeWritable")}</span>
        </button>
      </div>

      <p class="text-[11px] leading-relaxed text-[var(--text-muted)]">
        {props.mode === "writable"
          ? t("tasks.intakeScopeWritableHint")
          : t("tasks.intakeScopeReadOnlyHint")}
      </p>

      <Show when={props.mode === "writable"}>
        <input
          type="text"
          value={props.writableText}
          onInput={(e) => props.onWritableChange(e.currentTarget.value)}
          placeholder={t("tasks.intakeScopeWritablePlaceholder")}
          class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 font-mono text-[11.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
        />
        <div class="text-[10.5px] text-[var(--text-muted)]">
          {t("tasks.intakeScopeExtraReadOnlyLabel")}
        </div>
      </Show>

      <input
        type="text"
        value={props.readOnlyText}
        onInput={(e) => props.onReadOnlyChange(e.currentTarget.value)}
        placeholder={t("tasks.intakeScopeReadOnlyPlaceholder")}
        class="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 font-mono text-[11.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
      />
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
  const [scopeOpen, setScopeOpen] = createSignal(false);
  const [scopeMode, setScopeMode] = createSignal<PathScopeMode>("readonly");
  const [readOnlyInput, setReadOnlyInput] = createSignal("");
  const [writableInput, setWritableInput] = createSignal("");
  const [creating, setCreating] = createSignal(false);
  const [editingLinkID, setEditingLinkID] = createSignal<string | null>(null);
  const [editScopeMode, setEditScopeMode] = createSignal<PathScopeMode>("readonly");
  const [editReadOnlyInput, setEditReadOnlyInput] = createSignal("");
  const [editWritableInput, setEditWritableInput] = createSignal("");
  const [savingScope, setSavingScope] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const selectedExtraTab = createMemo(
    () => extraTabs().find((tab) => tab.id === activeTab()) ?? null
  );

  createEffect(() => {
    const dir = props.directory.trim();
    if (!props.open || !dir) {
      setError(null);
      setEditingLinkID(null);
      return;
    }
    setEditingLinkID(null);
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
      const readOnlyPaths = parseScopedPathInput(readOnlyInput());
      const writablePaths =
        scopeMode() === "writable" ? parseScopedPathInput(writableInput()) : [];
      await createIntakeLink({
        directory: dir,
        label: label().trim() || t("tasks.intakeLinksBtn"),
        readOnlyPaths: readOnlyPaths.length > 0 ? readOnlyPaths : undefined,
        writablePaths: writablePaths.length > 0 ? writablePaths : undefined,
      });
      setLabel("");
      setReadOnlyInput("");
      setWritableInput("");
      setScopeMode("readonly");
      setScopeOpen(false);
    } catch (err) {
      setError(formatIntakeError(err, "Failed to create link"));
    } finally {
      setCreating(false);
    }
  };

  const startEditScope = (item: IntakeLinkItem) => {
    setError(null);
    setEditingLinkID(item.id);
    const rw = item.writablePaths ?? [];
    const ro = item.readOnlyPaths ?? [];
    setEditScopeMode(rw.length > 0 ? "writable" : "readonly");
    setEditWritableInput(rw.join(", "));
    setEditReadOnlyInput(ro.join(", "));
  };

  const onSaveScope = async (item: IntakeLinkItem) => {
    if (savingScope()) return;
    setSavingScope(true);
    setError(null);
    try {
      const readOnlyPaths = parseScopedPathInput(editReadOnlyInput());
      const writablePaths =
        editScopeMode() === "writable" ? parseScopedPathInput(editWritableInput()) : [];
      await updateIntakeLink({
        id: item.id,
        directory: props.directory,
        readOnlyPaths,
        writablePaths,
      });
      setEditingLinkID(null);
    } catch (err) {
      setError(formatIntakeError(err, "Failed to update path boundaries"));
    } finally {
      setSavingScope(false);
    }
  };

  const onDelete = async (item: IntakeLinkItem) => {
    setError(null);
    if (editingLinkID() === item.id) {
      setEditingLinkID(null);
    }
    try {
      await removeIntakeLink(item.id, props.directory);
    } catch (err) {
      setError(formatIntakeError(err, "Failed to delete link"));
    }
  };

  const configuredScopeCount = createMemo(
    () =>
      parseScopedPathInput(readOnlyInput()).length +
      (scopeMode() === "writable" ? parseScopedPathInput(writableInput()).length : 0)
  );

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
                    <form onSubmit={(e) => void onCreate(e)} class="space-y-2.5">
                      <div class="flex items-center gap-2">
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
                      </div>

                      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 px-3 py-2">
                        <button
                          type="button"
                          onClick={() => setScopeOpen((prev) => !prev)}
                          class="flex w-full items-center justify-between gap-2 text-left text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                        >
                          <span class="inline-flex items-center gap-1.5">
                            <Lock size={12} class="shrink-0 text-[var(--accent-primary)]" />
                            <span>{t("tasks.intakeScopeTitle")}</span>
                            <Show when={configuredScopeCount() > 0}>
                              <span class="rounded bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--accent-primary)]">
                                {configuredScopeCount()}
                              </span>
                            </Show>
                          </span>
                          <ChevronDown
                            size={13}
                            class={`shrink-0 transition-transform duration-150 ${
                              scopeOpen() ? "rotate-180" : ""
                            }`}
                          />
                        </button>
                        <Show when={scopeOpen()}>
                          <div class="mt-2.5 border-t border-[var(--border-subtle)]/70 pt-2.5">
                            <PathScopeEditor
                              mode={scopeMode()}
                              onModeChange={setScopeMode}
                              readOnlyText={readOnlyInput()}
                              onReadOnlyChange={setReadOnlyInput}
                              writableText={writableInput()}
                              onWritableChange={setWritableInput}
                            />
                          </div>
                        </Show>
                      </div>
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
                                <div class="flex shrink-0 items-center gap-1">
                                  <button
                                    type="button"
                                    title={t("tasks.intakeScopeEditBtn")}
                                    onClick={() =>
                                      editingLinkID() === item.id
                                        ? setEditingLinkID(null)
                                        : startEditScope(item)
                                    }
                                    class="rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--text-primary)] cursor-pointer"
                                  >
                                    <Pencil size={12} />
                                  </button>
                                  <button
                                    type="button"
                                    onClick={() => void onDelete(item)}
                                    class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                                  >
                                    <Trash2 size={13} />
                                  </button>
                                </div>
                              </div>

                              <Show
                                when={
                                  editingLinkID() !== item.id &&
                                  ((item.writablePaths?.length ?? 0) > 0 ||
                                    (item.readOnlyPaths?.length ?? 0) > 0)
                                }
                              >
                                <div class="flex flex-wrap items-center gap-1.5">
                                  <Show when={(item.writablePaths?.length ?? 0) > 0}>
                                    <span class="inline-flex max-w-full items-center gap-1 truncate rounded-md border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/55 px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--accent-primary)]">
                                      <FolderLock size={10} class="shrink-0" />
                                      <span class="truncate">
                                        {t("tasks.intakeScopeWritableBadge", {
                                          paths: (item.writablePaths ?? []).join(", "),
                                        })}
                                      </span>
                                    </span>
                                  </Show>
                                  <Show when={(item.readOnlyPaths?.length ?? 0) > 0}>
                                    <span class="inline-flex max-w-full items-center gap-1 truncate rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-secondary)]">
                                      <Lock size={10} class="shrink-0" />
                                      <span class="truncate">
                                        {t("tasks.intakeScopeReadOnlyBadge", {
                                          paths: (item.readOnlyPaths ?? []).join(", "),
                                        })}
                                      </span>
                                    </span>
                                  </Show>
                                </div>
                              </Show>

                              <Show when={editingLinkID() === item.id}>
                                <div class="space-y-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 p-2.5">
                                  <PathScopeEditor
                                    mode={editScopeMode()}
                                    onModeChange={setEditScopeMode}
                                    readOnlyText={editReadOnlyInput()}
                                    onReadOnlyChange={setEditReadOnlyInput}
                                    writableText={editWritableInput()}
                                    onWritableChange={setEditWritableInput}
                                  />
                                  <div class="flex items-center justify-end gap-1.5">
                                    <button
                                      type="button"
                                      onClick={() => setEditingLinkID(null)}
                                      class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                                    >
                                      {t("tasks.intakeScopeCancelBtn")}
                                    </button>
                                    <button
                                      type="button"
                                      disabled={savingScope()}
                                      onClick={() => void onSaveScope(item)}
                                      class="rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
                                    >
                                      {savingScope()
                                        ? t("tasks.intakeScopeSavingBtn")
                                        : t("tasks.intakeScopeSaveBtn")}
                                    </button>
                                  </div>
                                </div>
                              </Show>

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
