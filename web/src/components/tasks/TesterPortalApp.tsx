// web/src/components/tasks/TesterPortalApp.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  lazy,
  Match,
  onCleanup,
  onMount,
  Show,
  Suspense,
  Switch,
} from "solid-js";
import {
  CheckCircle2,
  Clock,
  LoaderCircle,
  MinusCircle,
  Plus,
  RotateCcw,
  ShieldAlert,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  activePortalTask,
  activePortalTaskID,
  imageViewerOpen,
  mountTesterPortal,
  openImagePreview,
  portalAuthFailed,
  portalConnected,
  portalError,
  portalProjectName,
  portalTasks,
  portalTesterLabel,
  portalTimeline,
  selectPortalTask,
  type IntakeAttachmentRef,
  type IntakeTaskComment,
  type IntakeTaskStatus,
} from "../../store/workspace";
import MarkdownView from "../timeline/MarkdownView";
import PortalExecutionTimeline, { PortalDiffSummaryBar } from "./PortalExecutionTimeline";
import {
  buildPortalConversationEntries,
  selectMatchingPortalTimeline,
  summarizeTimelineDiff,
} from "./portal-timeline-helpers";
import {
  TesterFloatingFeedbackComposer,
  TesterNewTaskComposer,
} from "./TesterPortalComposer";

const ImagePreviewOverlay = lazy(() => import("../preview/ImagePreviewOverlay"));
import AttachmentContextMenu from "../preview/AttachmentContextMenu";

function portalStatusText(status: IntakeTaskStatus): string {
  switch (status) {
    case "staged":
      return t("tasks.intakeStatusStaged");
    case "running":
      return t("tasks.intakeStatusRunning");
    case "succeeded":
      return t("tasks.intakeStatusSucceeded");
    case "awaiting_retest":
      return t("tasks.intakeStatusRetest");
    case "reopened":
      return t("tasks.intakeStatusReopened");
    case "resolved":
      return t("tasks.intakeStatusResolved");
    case "failed":
      return t("tasks.intakeStatusFailed");
    default:
      return t("tasks.intakeStatusRejected");
  }
}

function portalStatusTone(status: IntakeTaskStatus): string {
  switch (status) {
    case "running":
      return "bg-[var(--accent-surface)] text-[var(--accent-primary)]";
    case "succeeded":
    case "awaiting_retest":
    case "resolved":
      return "bg-[var(--ok-surface)] text-[var(--ok-text)]";
    case "failed":
    case "reopened":
      return "bg-[var(--danger-surface)] text-[var(--danger-text)]";
    default:
      return "bg-[var(--bg-inset)] text-[var(--text-secondary)]";
  }
}

function PortalAttachmentGallery(props: { items: IntakeAttachmentRef[] }) {
  return (
    <Show when={props.items.length > 0}>
      <div class="flex flex-wrap gap-2 pt-1">
        <For each={props.items}>
          {(att) => (
            <Show
              when={att.mime.startsWith("image/") && att.url}
              fallback={
                <a
                  href={att.url}
                  target="_blank"
                  rel="noreferrer"
                  class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1 font-mono text-[11px] text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--accent-primary)] transition-colors"
                >
                  {att.filename}
                </a>
              }
            >
              <button
                type="button"
                onClick={() => openImagePreview({ src: att.url!, alt: att.filename })}
                class="overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:border-[var(--accent-border)] transition-colors cursor-zoom-in"
              >
                <img
                  src={att.url}
                  alt={att.filename}
                  class="h-20 w-28 object-cover"
                />
              </button>
            </Show>
          )}
        </For>
      </div>
    </Show>
  );
}

function PortalCommentEntry(props: { comment: IntakeTaskComment }) {
  return (
    <Switch>
      {/* Developer reply: borderless left-accent note, matching desktop prose tone */}
      <Match when={props.comment.authorRole === "developer"}>
        <div class="border-l-2 border-[var(--ok-text)]/50 pl-3.5 py-0.5 space-y-1.5">
          <div class="text-[11px] font-medium text-[var(--ok-text)]">
            {t("tasks.portalDeveloperNoteLabel")}
          </div>
          <Show when={props.comment.text}>
            <div class="text-[var(--text-primary)]">
              <MarkdownView content={props.comment.text} density="chat" />
            </div>
          </Show>
          <PortalAttachmentGallery items={props.comment.attachments ?? []} />
        </div>
      </Match>

      {/* Retest passed milestone: quiet inline confirmation, no card wrapper */}
      <Match when={props.comment.action === "pass"}>
        <div class="space-y-1 py-1">
          <div class="inline-flex items-center gap-1.5 text-[12px] font-medium text-[var(--ok-text)]">
            <CheckCircle2 size={13.5} class="shrink-0" />
            <span>{t("tasks.portalRetestPassedLabel")}</span>
          </div>
          <Show when={props.comment.text}>
            <div class="pl-5 text-[var(--text-secondary)]">
              <MarkdownView content={props.comment.text} density="compact" />
            </div>
          </Show>
          <Show when={(props.comment.attachments ?? []).length > 0}>
            <div class="pl-5">
              <PortalAttachmentGallery items={props.comment.attachments ?? []} />
            </div>
          </Show>
        </div>
      </Match>

      {/* Tester follow-up / reopen: right-aligned soft bubble without heavy border, matching desktop UserTurnItem */}
      <Match when={true}>
        <div class="flex flex-col items-end">
          <div
            class={`max-w-[85%] rounded-2xl px-3.5 py-2.5 space-y-1.5 ${
              props.comment.action === "reopen"
                ? "bg-[var(--danger-surface)]/60"
                : "bg-[var(--accent-surface)]"
            }`}
          >
            <Show when={props.comment.action === "reopen"}>
              <div class="inline-flex items-center gap-1 text-[11px] font-medium text-[var(--danger-text)]">
                <RotateCcw size={11} class="shrink-0" />
                <span>{t("tasks.portalRetestReopenLabel")}</span>
              </div>
            </Show>
            <Show when={props.comment.text}>
              <div class="text-[var(--text-primary)]">
                <MarkdownView content={props.comment.text} density="chat" />
              </div>
            </Show>
            <PortalAttachmentGallery items={props.comment.attachments ?? []} />
          </div>
        </div>
      </Match>
    </Switch>
  );
}

export default function TesterPortalApp() {
  const [floatingHeight, setFloatingHeight] = createSignal(148);
  const [stickToBottom, setStickToBottom] = createSignal(true);
  const imageViewerEverOpened = createMemo<boolean>(
    (prev) => prev || Boolean(imageViewerOpen()),
    false
  );

  let scrollerRef: HTMLDivElement | undefined;

  onMount(() => {
    const cleanup = mountTesterPortal();
    onCleanup(cleanup);
  });

  const matchedTimeline = createMemo(() =>
    selectMatchingPortalTimeline(activePortalTask()?.id, portalTimeline())
  );
  const timelineTurns = createMemo(() => matchedTimeline()?.turns ?? []);
  const timelineDiff = createMemo(() => summarizeTimelineDiff(timelineTurns()));
  const isLiveBusy = createMemo(() => {
    const cur = activePortalTask();
    return Boolean((cur && cur.status === "running") || matchedTimeline()?.busy);
  });
  const hasFloatingComposer = createMemo(() => {
    const cur = activePortalTask();
    return Boolean(cur && cur.status !== "resolved" && cur.status !== "rejected");
  });

  const handleScroll = () => {
    if (!scrollerRef) return;
    const distance =
      scrollerRef.scrollHeight - scrollerRef.scrollTop - scrollerRef.clientHeight;
    setStickToBottom(distance <= 96);
  };

  // Reset scroll position when switching tasks.
  createEffect((prevId?: string | null) => {
    const id = activePortalTaskID();
    if (prevId !== undefined && prevId !== id && scrollerRef) {
      scrollerRef.scrollTop = 0;
      setStickToBottom(true);
    }
    return id;
  });

  // Auto-follow streaming output only when user is already near the bottom.
  createEffect(() => {
    const tl = matchedTimeline();
    if (!tl || !isLiveBusy() || !stickToBottom() || !scrollerRef) return;
    const raf = requestAnimationFrame(() => {
      if (scrollerRef && stickToBottom()) {
        scrollerRef.scrollTop = scrollerRef.scrollHeight;
      }
    });
    onCleanup(() => cancelAnimationFrame(raf));
  });

  return (
    <div class="hoxi-shell-root flex h-screen w-screen overflow-hidden bg-[var(--bg-canvas)] text-[var(--text-primary)]">
      <Show
        when={!portalAuthFailed()}
        fallback={
          <div class="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
            <ShieldAlert size={36} class="text-[var(--danger-text)]" />
            <h1 class="text-base font-semibold">{t("tasks.portalInvalidTitle")}</h1>
            <p class="max-w-sm text-xs text-[var(--text-muted)]">
              {t("tasks.portalInvalidDesc")}
            </p>
          </div>
        }
      >
        {/* Left pane: flat borderless project header + task list */}
        <aside class="flex w-64 sm:w-72 shrink-0 flex-col px-3 py-3">
          <div class="border-b border-[var(--border-subtle)]/50 pb-3">
            <div class="flex items-center justify-between gap-2 px-1">
              <span class="truncate text-[13.5px] font-semibold text-[var(--text-primary)]">
                {portalProjectName() || t("tasks.portalBadge")}
              </span>
              <span class="inline-flex shrink-0 items-center gap-1 text-[10.5px] text-[var(--text-muted)]">
                <span
                  class={`h-1.5 w-1.5 rounded-full ${
                    portalConnected() ? "bg-[var(--ok-text)]" : "bg-[var(--text-muted)]"
                  }`}
                />
                {portalConnected()
                  ? t("tasks.portalLiveConnected")
                  : t("tasks.portalLiveReconnecting")}
              </span>
            </div>
            <Show when={portalTesterLabel()}>
              <p class="mt-0.5 truncate px-1 text-[11px] text-[var(--text-muted)]">
                {portalTesterLabel()}
              </p>
            </Show>
            <button
              type="button"
              onClick={() => selectPortalTask(null)}
              class="mt-3 flex w-full items-center justify-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-2 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
            >
              <Plus size={14} />
              <span>{t("tasks.portalNewTaskBtn")}</span>
            </button>
          </div>

          <div class="mt-2.5 min-h-0 flex-1 overflow-y-auto py-1">
            <p class="mb-1.5 px-2 text-[11px] font-medium text-[var(--text-faint)]">
              {t("tasks.portalTaskListTitle")}
            </p>
            <Show
              when={portalTasks().length > 0}
              fallback={
                <p class="px-2 py-6 text-center text-[12px] text-[var(--text-faint)]">
                  {t("tasks.portalTaskListEmpty")}
                </p>
              }
            >
              <div class="space-y-0.5">
                <For each={portalTasks()}>
                  {(item) => (
                    <button
                      type="button"
                      onClick={() => selectPortalTask(item.id)}
                      class={`flex w-full flex-col gap-0.5 rounded-lg px-2.5 py-2 text-left transition-colors cursor-pointer ${
                        activePortalTaskID() === item.id
                          ? "bg-[var(--bg-selected)] text-[var(--text-primary)]"
                          : "hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
                      }`}
                    >
                      <div class="flex items-center justify-between gap-2">
                        <span class="min-w-0 flex-1 truncate text-[12.5px] font-medium text-[var(--text-primary)]">
                          {item.title}
                        </span>
                        <Show when={item.status === "running"}>
                          <LoaderCircle size={12} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
                        </Show>
                      </div>
                      <div class="flex items-center justify-between text-[10.5px] text-[var(--text-muted)]">
                        <span>{portalStatusText(item.status)}</span>
                        <Show when={item.status === "rejected"}>
                          <MinusCircle size={11} />
                        </Show>
                      </div>
                    </button>
                  )}
                </For>
              </div>
            </Show>
          </div>
        </aside>

        {/* Center main pane-island: direct canvas prose + folded progress + floating bottom glass composer */}
        <main class="pane-island relative my-2 mr-2 flex min-w-0 flex-1 flex-col overflow-hidden">
          <div
            ref={scrollerRef}
            onScroll={handleScroll}
            data-markdown-reader-root="true"
            class="flex min-h-0 flex-1 flex-col overflow-y-auto"
          >
            <div
              aria-hidden="true"
              data-portal-scroll-fade="true"
              class="pointer-events-none sticky top-0 z-10 -mb-6 h-6 w-full shrink-0 bg-gradient-to-b from-[var(--bg-panel)] to-transparent"
            />
            <div
              class="mx-auto flex w-full max-w-[var(--thread-max-width)] flex-1 flex-col px-5 sm:px-8 pt-7"
              style={{
                "padding-bottom": hasFloatingComposer()
                  ? `${Math.max(152, floatingHeight() + 24)}px`
                  : "40px",
              }}
            >
              <Show when={portalError()}>
                <div class="mb-4 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-4 py-2.5 text-[12px] text-[var(--danger-text)]">
                  {portalError()}
                </div>
              </Show>

              <Show
                when={activePortalTask()}
                fallback={<TesterNewTaskComposer />}
              >
                {(task) => {
                  const followupComments = createMemo(() =>
                    (task().comments ?? []).filter((c) => c.action !== "submit")
                  );
                  const entries = createMemo(() =>
                    buildPortalConversationEntries(timelineTurns(), followupComments())
                  );
                  const lastExecutionIndex = createMemo(() => {
                    const list = entries();
                    for (let i = list.length - 1; i >= 0; i--) {
                      if (list[i].kind === "execution") return i;
                    }
                    return -1;
                  });
                  const standaloneDeveloperNote = createMemo(() => {
                    const note = task().developerNote?.trim();
                    if (!note) return "";
                    const duplicated = followupComments().some(
                      (c) => c.authorRole === "developer" && c.text?.trim() === note
                    );
                    return duplicated ? "" : note;
                  });
                  const isWaitingApproval = () =>
                    task().status === "staged" || task().status === "reopened";

                  return (
                    <div class="space-y-6">
                      {/* Clean issue header rendered directly on the canvas (no outer box) */}
                      <header class="space-y-3">
                        <div class="flex items-start justify-between gap-3">
                          <h2 class="text-[18px] font-semibold leading-snug tracking-tight text-[var(--text-primary)] selectable-text">
                            {task().title}
                          </h2>
                          <span
                            class={`inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[11px] font-medium ${portalStatusTone(
                              task().status
                            )}`}
                          >
                            <Show when={isLiveBusy()}>
                              <LoaderCircle size={11} class="animate-spin" />
                            </Show>
                            <span>{portalStatusText(task().status)}</span>
                          </span>
                        </div>

                        <PortalDiffSummaryBar diff={timelineDiff()} scopeKey={task().id} />

                        <Show when={task().description}>
                          <div class="text-[var(--text-primary)]">
                            <MarkdownView content={task().description} density="chat" />
                          </div>
                        </Show>

                        <Show when={task().steps}>
                          <div class="border-l-2 border-[var(--border-subtle)] pl-3.5 py-0.5 space-y-1">
                            <div class="text-[11px] font-medium text-[var(--text-muted)]">
                              {t("tasks.portalStepsLabel")}
                            </div>
                            <div class="text-[var(--text-secondary)]">
                              <MarkdownView content={task().steps!} density="compact" />
                            </div>
                          </div>
                        </Show>

                        <PortalAttachmentGallery items={task().attachments ?? []} />

                        <Show when={task().status === "failed" && task().error}>
                          <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-2.5 text-[12px] text-[var(--danger-text)]">
                            {task().error}
                          </div>
                        </Show>
                      </header>

                      {/* Unified chronological conversation & execution stream */}
                      <section class="border-t border-[var(--border-subtle)]/55 pt-5 space-y-5">
                        <Show
                          when={entries().length > 0}
                          fallback={
                            <Show
                              when={task().summary}
                              fallback={
                                <Show
                                  when={!isWaitingApproval()}
                                  fallback={
                                    <div class="flex items-center gap-2 rounded-lg bg-[var(--bg-inset)]/55 px-3 py-2 text-[12px] text-[var(--text-muted)]">
                                      <Clock size={14} class="shrink-0 text-[var(--accent-primary)]" />
                                      <span>
                                        {task().status === "reopened"
                                          ? t("tasks.portalReopenedBanner")
                                          : t("tasks.portalStagedBanner")}
                                      </span>
                                    </div>
                                  }
                                >
                                  <p class="py-6 text-center text-[12px] text-[var(--text-faint)]">
                                    {t("tasks.portalProcessWaiting")}
                                  </p>
                                </Show>
                              }
                            >
                              <div class="text-[var(--text-primary)]">
                                <MarkdownView content={task().summary!} density="chat" />
                              </div>
                            </Show>
                          }
                        >
                          <For each={entries()}>
                            {(entry, idx) => (
                              <Show
                                when={entry.kind === "execution"}
                                fallback={
                                  <PortalCommentEntry
                                    comment={
                                      (entry as Extract<typeof entry, { kind: "comment" }>).comment
                                    }
                                  />
                                }
                              >
                                <PortalExecutionTimeline
                                  turns={
                                    (entry as Extract<typeof entry, { kind: "execution" }>).turns
                                  }
                                  busy={isLiveBusy() && idx() === lastExecutionIndex()}
                                  isLastBlock={idx() === lastExecutionIndex()}
                                  scopeKey={task().id}
                                />
                              </Show>
                            )}
                          </For>

                          <Show when={isWaitingApproval()}>
                            <div class="flex items-center gap-2 rounded-lg bg-[var(--bg-inset)]/55 px-3 py-2 text-[12px] text-[var(--text-muted)]">
                              <Clock size={14} class="shrink-0 text-[var(--accent-primary)]" />
                              <span>
                                {task().status === "reopened"
                                  ? t("tasks.portalReopenedBanner")
                                  : t("tasks.portalStagedBanner")}
                              </span>
                            </div>
                          </Show>
                        </Show>

                        <Show when={standaloneDeveloperNote()}>
                          <div class="border-l-2 border-[var(--ok-text)]/50 pl-3.5 py-0.5 space-y-1.5">
                            <div class="text-[11px] font-medium text-[var(--ok-text)]">
                              {t("tasks.portalDeveloperNoteLabel")}
                            </div>
                            <div class="text-[var(--text-primary)]">
                              <MarkdownView content={standaloneDeveloperNote()} density="chat" />
                            </div>
                          </div>
                        </Show>
                      </section>
                    </div>
                  );
                }}
              </Show>
            </div>
          </div>

          {/* Floating glass follow-up & retest composer pinned at the bottom of the pane */}
          <Show when={activePortalTask()}>
            {(task) => (
              <Show when={task().status !== "resolved" && task().status !== "rejected"}>
                <TesterFloatingFeedbackComposer
                  taskId={task().id}
                  status={task().status}
                  onHeightChange={setFloatingHeight}
                />
              </Show>
            )}
          </Show>
        </main>

        <AttachmentContextMenu />
        <Suspense>
          <Show when={imageViewerEverOpened()}>
            <ImagePreviewOverlay />
          </Show>
        </Suspense>
      </Show>
    </div>
  );
}
