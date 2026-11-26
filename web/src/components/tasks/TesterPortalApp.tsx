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
  CircleDashed,
  Clock,
  FileCode2,
  ListTodo,
  LoaderCircle,
  MessageSquare,
  MinusCircle,
  Plus,
  Search,
  ShieldAlert,
  Terminal,
  Wrench,
  XCircle,
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
  type SanitizedExecutionStep,
} from "../../store/workspace";
import MarkdownInline from "../timeline/MarkdownInline";
import MarkdownView from "../timeline/MarkdownView";
import {
  TesterFloatingFeedbackComposer,
  TesterNewTaskComposer,
} from "./TesterPortalComposer";

const ImagePreviewOverlay = lazy(() => import("../preview/ImagePreviewOverlay"));

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

function commentBadgeLabel(comment: IntakeTaskComment): string {
  if (comment.authorRole === "developer") {
    return t("tasks.portalDeveloperNoteLabel");
  }
  if (comment.action === "reopen") {
    return t("tasks.portalRetestReopenLabel");
  }
  if (comment.action === "pass") {
    return t("tasks.portalRetestPassedLabel");
  }
  return comment.authorLabel || t("tasks.portalTesterCommentLabel");
}

function StepCategoryIcon(props: { category?: string; status?: string }) {
  return (
    <Switch>
      <Match when={props.status === "running" || props.status === "pending"}>
        <LoaderCircle size={14} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
      </Match>
      <Match when={props.status === "error"}>
        <XCircle size={14} class="shrink-0 text-[var(--danger-text)]" />
      </Match>
      <Match when={props.category === "search"}>
        <Search size={13} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
      <Match when={props.category === "command"}>
        <Terminal size={13} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
      <Match when={props.category === "edit"}>
        <Wrench size={13} class="shrink-0 text-[var(--accent-primary)]" />
      </Match>
      <Match when={true}>
        <FileCode2 size={13} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
    </Switch>
  );
}

function formatStepDuration(start?: number, end?: number): string {
  if (!start || !end || end <= start) return "";
  const sec = (end - start) / 1000;
  return sec < 10 ? `${sec.toFixed(1)}s` : `${Math.round(sec)}s`;
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

// Non-expandable execution step row: renders Markdown explanations & status without exposing source code or diffs.
function SanitizedStepRow(props: { step: SanitizedExecutionStep; streaming?: boolean }) {
  const duration = () => formatStepDuration(props.step.startTime, props.step.endTime);

  return (
    <Switch>
      <Match when={props.step.kind === "text" && props.step.text}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/65 px-4 py-3 text-[var(--text-primary)]">
          <MarkdownView
            content={props.step.text!}
            density="chat"
            streaming={props.streaming}
          />
        </div>
      </Match>
      <Match when={props.step.kind === "todo" && (props.step.todos ?? []).length > 0}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-2.5">
          <div class="mb-1.5 flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
            <ListTodo size={13} class="text-[var(--accent-primary)]" />
            <span>{props.step.label || t("tasks.groupTodos")}</span>
          </div>
          <div class="space-y-1">
            <For each={props.step.todos ?? []}>
              {(todo) => (
                <div class="flex items-start gap-2 text-[12.5px] leading-[1.55] text-[var(--text-secondary)]">
                  <span class="mt-0.5 shrink-0">
                    <Show
                      when={todo.status === "completed"}
                      fallback={
                        <Show
                          when={todo.status === "in_progress"}
                          fallback={<CircleDashed size={12} class="text-[var(--text-muted)]" />}
                        >
                          <LoaderCircle size={12} class="animate-spin text-[var(--accent-primary)]" />
                        </Show>
                      }
                    >
                      <CheckCircle2 size={12} class="text-[var(--ok-text)]" />
                    </Show>
                  </span>
                  <div
                    class={`min-w-0 flex-1 break-words ${
                      todo.status === "completed" ? "line-through opacity-75" : ""
                    }`}
                  >
                    <MarkdownInline text={todo.content} />
                  </div>
                </div>
              )}
            </For>
          </div>
        </div>
      </Match>
      <Match when={props.step.kind === "tool"}>
        <div class="flex select-none items-center justify-between gap-2 rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/70 px-3 py-1.5">
          <div class="flex min-w-0 items-center gap-2">
            <StepCategoryIcon category={props.step.category} status={props.step.status} />
            <span class="truncate text-[12px] text-[var(--text-secondary)]">
              {props.step.label}
            </span>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Show when={duration()}>
              <span class="font-mono text-[10px] text-[var(--text-faint)]">{duration()}</span>
            </Show>
            <Show when={props.step.status === "completed"}>
              <CheckCircle2 size={12} class="text-[var(--ok-text)]" />
            </Show>
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

  const timelineTurns = createMemo(() => portalTimeline()?.turns ?? []);
  const isLiveBusy = createMemo(() => {
    const cur = activePortalTask();
    return Boolean((cur && cur.status === "running") || portalTimeline()?.busy);
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
    const tl = portalTimeline();
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
        {/* Left pane: project header + submitted tasks list (NO right-side code inspector) */}
        <aside class="flex w-64 sm:w-72 shrink-0 flex-col px-2.5 py-2.5">
          <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3.5 py-3 shadow-[var(--elevation-1)]">
            <div class="flex items-center justify-between gap-2">
              <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                {portalProjectName() || t("tasks.portalBadge")}
              </span>
              <span class="inline-flex shrink-0 items-center gap-1 rounded-full bg-[var(--bg-inset)] px-2 py-0.5 text-[10px] text-[var(--text-muted)]">
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
              <p class="mt-0.5 truncate text-[11px] text-[var(--text-muted)]">
                {portalTesterLabel()}
              </p>
            </Show>
            <button
              type="button"
              onClick={() => selectPortalTask(null)}
              class="mt-2.5 flex w-full items-center justify-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-2 text-[12px] font-medium text-[var(--accent-foreground)] shadow-xs hover:opacity-90 transition-opacity cursor-pointer"
            >
              <Plus size={14} />
              <span>{t("tasks.portalNewTaskBtn")}</span>
            </button>
          </div>

          <div class="mt-2.5 min-h-0 flex-1 overflow-y-auto px-0.5 py-1">
            <p class="mb-1.5 px-2 text-[11px] font-medium text-[var(--text-muted)]">
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
              <div class="space-y-1">
                <For each={portalTasks()}>
                  {(item) => (
                    <button
                      type="button"
                      onClick={() => selectPortalTask(item.id)}
                      class={`flex w-full flex-col gap-1 rounded-xl px-3 py-2 text-left transition-colors cursor-pointer ${
                        activePortalTaskID() === item.id
                          ? "bg-[var(--bg-selected)] text-[var(--text-primary)] ring-1 ring-[var(--accent-border)]"
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

        {/* Center main pane-island: scrollable Markdown stream + floating bottom glass composer */}
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
              class="mx-auto flex w-full max-w-[calc(var(--thread-max-width)+3rem)] flex-1 flex-col space-y-4 px-4 sm:px-6 pt-6"
              style={{
                "padding-bottom": hasFloatingComposer()
                  ? `${Math.max(152, floatingHeight() + 24)}px`
                  : "32px",
              }}
            >
              <Show when={portalError()}>
                <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-4 py-2.5 text-[12px] text-[var(--danger-text)]">
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
                  return (
                    <div class="space-y-4">
                      {/* Task summary card with full Markdown support */}
                      <div class="space-y-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/55 p-5">
                        <div class="flex items-start justify-between gap-3">
                          <h2 class="text-[16px] font-semibold tracking-tight text-[var(--text-primary)] selectable-text">
                            {task().title}
                          </h2>
                          <span class="shrink-0 rounded-full bg-[var(--accent-surface)] px-2.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)]">
                            {portalStatusText(task().status)}
                          </span>
                        </div>

                        <Show when={task().description}>
                          <div class="text-[var(--text-primary)]">
                            <MarkdownView content={task().description} density="chat" />
                          </div>
                        </Show>

                        <Show when={task().steps}>
                          <div class="rounded-xl border border-[var(--border-subtle)]/75 bg-[var(--bg-inset)] px-3.5 py-2.5">
                            <div class="mb-1 text-[11px] font-medium text-[var(--text-muted)]">
                              {t("tasks.portalStepsLabel")}
                            </div>
                            <MarkdownView content={task().steps!} density="compact" />
                          </div>
                        </Show>

                        <PortalAttachmentGallery items={task().attachments ?? []} />

                        <Show
                          when={
                            Boolean(task().developerNote?.trim()) &&
                            !followupComments().some(
                              (c) =>
                                c.authorRole === "developer" &&
                                c.text?.trim() === task().developerNote?.trim()
                            )
                          }
                        >
                          <div class="rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)]/75 px-3.5 py-2.5">
                            <div class="mb-1 text-[11px] font-semibold text-[var(--ok-text)]">
                              {t("tasks.portalDeveloperNoteLabel")}
                            </div>
                            <MarkdownView content={task().developerNote!} density="compact" />
                          </div>
                        </Show>

                        <Show when={task().status === "failed" && task().error}>
                          <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-2.5 text-[12px] text-[var(--danger-text)]">
                            {task().error}
                          </div>
                        </Show>
                      </div>

                      {/* Staged / waiting notice */}
                      <Show when={task().status === "staged" || task().status === "reopened"}>
                        <div class="flex items-center gap-2.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-4 py-3 text-[12px] text-[var(--text-secondary)]">
                          <Clock size={15} class="shrink-0 text-[var(--accent-primary)]" />
                          <span>
                            {task().status === "reopened"
                              ? t("tasks.portalReopenedBanner")
                              : t("tasks.portalStagedBanner")}
                          </span>
                        </div>
                      </Show>

                      {/* Sanitized Live Execution Process (hidden while staged/reopened with no output yet) */}
                      <Show
                        when={
                          !(
                            (task().status === "staged" || task().status === "reopened") &&
                            timelineTurns().length === 0 &&
                            !task().summary
                          )
                        }
                      >
                      <div class="space-y-2.5 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-5">
                        <div class="flex items-center justify-between">
                          <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
                            {t("tasks.portalProcessTitle")}
                          </h3>
                          <Show when={isLiveBusy()}>
                            <LoaderCircle size={14} class="animate-spin text-[var(--accent-primary)]" />
                          </Show>
                        </div>

                        <Show
                          when={timelineTurns().length > 0}
                          fallback={
                            <Show
                              when={task().summary}
                              fallback={
                                <p class="py-6 text-center text-[12px] text-[var(--text-faint)]">
                                  {t("tasks.portalProcessWaiting")}
                                </p>
                              }
                            >
                              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/65 px-4 py-3 text-[var(--text-primary)]">
                                <MarkdownView content={task().summary!} density="chat" />
                              </div>
                            </Show>
                          }
                        >
                          <div class="space-y-2.5">
                            <For each={timelineTurns()}>
                              {(turn, turnIdx) => (
                                <div class="space-y-1.5">
                                  <For each={turn.steps}>
                                    {(step, stepIdx) => (
                                      <SanitizedStepRow
                                        step={step}
                                        streaming={
                                          isLiveBusy() &&
                                          turnIdx() === timelineTurns().length - 1 &&
                                          stepIdx() === turn.steps.length - 1
                                        }
                                      />
                                    )}
                                  </For>
                                </div>
                              )}
                            </For>
                          </div>
                        </Show>
                      </div>
                      </Show>

                      {/* Follow-up & retest conversation thread */}
                      <Show when={followupComments().length > 0}>
                        <div class="space-y-2.5 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-5">
                          <div class="flex items-center gap-1.5 text-[13px] font-semibold text-[var(--text-primary)]">
                            <MessageSquare size={14} class="text-[var(--accent-primary)]" />
                            <h3>{t("tasks.portalCommentsTitle")}</h3>
                          </div>
                          <div class="space-y-2.5">
                            <For each={followupComments()}>
                              {(comment) => (
                                <div
                                  class={`space-y-1.5 rounded-xl border px-3.5 py-2.5 ${
                                    comment.authorRole === "developer"
                                      ? "border-[var(--ok-border)] bg-[var(--ok-surface)]/55"
                                      : comment.action === "reopen"
                                        ? "border-[var(--danger-border)]/70 bg-[var(--danger-surface)]/45"
                                        : "border-[var(--border-subtle)] bg-[var(--bg-elevated)]/65"
                                  }`}
                                >
                                  <div class="flex items-center justify-between gap-2 text-[11px]">
                                    <span class="font-semibold text-[var(--text-primary)]">
                                      {commentBadgeLabel(comment)}
                                    </span>
                                  </div>
                                  <Show when={comment.text}>
                                    <MarkdownView content={comment.text} density="compact" />
                                  </Show>
                                  <PortalAttachmentGallery items={comment.attachments ?? []} />
                                </div>
                              )}
                            </For>
                          </div>
                        </div>
                      </Show>
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

        <Suspense>
          <Show when={imageViewerEverOpened()}>
            <ImagePreviewOverlay />
          </Show>
        </Suspense>
      </Show>
    </div>
  );
}
