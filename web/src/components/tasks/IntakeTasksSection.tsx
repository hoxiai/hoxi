// web/src/components/tasks/IntakeTasksSection.tsx
import { createMemo, createSignal, For, Match, Show, Switch } from "solid-js";
import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDashed,
  ClipboardCheck,
  LoaderCircle,
  MinusCircle,
  Play,
  Send,
  Square,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  approveIntakeTask,
  approveIntakeTasksBatch,
  formatIntakeError,
  intakeTasks,
  isIntakeTaskExpanded,
  openImagePreview,
  selectSession,
  toggleIntakeTaskExpanded,
  updateIntakeTaskStatus,
  type IntakeTaskItem,
  type IntakeTaskStatus,
} from "../../store/workspace";

function intakeStatusLabel(status: IntakeTaskStatus): string {
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

function intakeStatusColor(status: IntakeTaskStatus): string {
  switch (status) {
    case "running":
      return "text-[var(--accent-primary)]";
    case "succeeded":
    case "awaiting_retest":
    case "resolved":
      return "text-[var(--ok-text)]";
    case "failed":
    case "reopened":
      return "text-[var(--danger-text)]";
    default:
      return "text-[var(--text-muted)]";
  }
}

function IntakeStatusIcon(props: { status: IntakeTaskStatus }) {
  return (
    <Switch>
      <Match when={props.status === "running"}>
        <LoaderCircle size={12} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
      </Match>
      <Match
        when={
          props.status === "succeeded" ||
          props.status === "awaiting_retest" ||
          props.status === "resolved"
        }
      >
        <CheckCircle2 size={12} class="shrink-0 text-[var(--ok-text)]" />
      </Match>
      <Match when={props.status === "failed" || props.status === "reopened"}>
        <XCircle size={12} class="shrink-0 text-[var(--danger-text)]" />
      </Match>
      <Match when={props.status === "rejected"}>
        <MinusCircle size={12} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
      <Match when={true}>
        <CircleDashed size={12} class="shrink-0 text-[var(--accent-primary)]" />
      </Match>
    </Switch>
  );
}

function IntakeTaskCard(props: { item: IntakeTaskItem }) {
  const [busy, setBusy] = createSignal(false);
  const [noteOpen, setNoteOpen] = createSignal(false);
  const [devNote, setDevNote] = createSignal("");
  const [error, setError] = createSignal<string | null>(null);

  const expanded = () => isIntakeTaskExpanded(props.item.id, props.item.status);
  const canApprove = () =>
    props.item.status === "staged" ||
    props.item.status === "reopened" ||
    props.item.status === "failed";

  const latestTesterFollowup = createMemo(() => {
    const comments = props.item.comments ?? [];
    for (let i = comments.length - 1; i >= 0; i--) {
      if (comments[i].authorRole === "tester" && comments[i].action !== "submit") {
        return comments[i];
      }
    }
    return null;
  });

  const onApprove = async () => {
    if (busy()) return;
    setBusy(true);
    setError(null);
    try {
      await approveIntakeTask(props.item.id);
    } catch (err) {
      setError(formatIntakeError(err, "Approve failed"));
    } finally {
      setBusy(false);
    }
  };

  const onSetStatus = async (
    status: "awaiting_retest" | "rejected" | "resolved" | "cancel",
    note?: string
  ) => {
    if (busy()) return;
    setBusy(true);
    setError(null);
    try {
      await updateIntakeTaskStatus({
        taskId: props.item.id,
        status,
        developerNote: note?.trim() || undefined,
      });
      setNoteOpen(false);
      setDevNote("");
    } catch (err) {
      setError(formatIntakeError(err, "Status update failed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="mb-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 px-2 py-1.5">
      <div class="flex items-center gap-1.5">
        <button
          type="button"
          onClick={() => toggleIntakeTaskExpanded(props.item.id, props.item.status)}
          class="shrink-0 rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
        >
          {expanded() ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
        </button>
        <IntakeStatusIcon status={props.item.status} />
        <Show
          when={props.item.sessionID}
          fallback={
            <span class="min-w-0 flex-1 truncate text-[12px] font-medium text-[var(--text-primary)]">
              {props.item.title}
            </span>
          }
        >
          <button
            type="button"
            onClick={() => void selectSession(props.item.sessionID!)}
            title={t("tasks.openSession")}
            class="min-w-0 flex-1 truncate text-left text-[12px] font-medium text-[var(--text-primary)] hover:text-[var(--accent-primary)] cursor-pointer"
          >
            {props.item.title}
          </button>
        </Show>
        <span class="shrink-0 rounded bg-[var(--bg-panel)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
          {props.item.submitterLabel}
        </span>
        <span class={`shrink-0 text-[10px] ${intakeStatusColor(props.item.status)}`}>
          {intakeStatusLabel(props.item.status)}
        </span>
      </div>

      <Show when={expanded()}>
        <div class="mt-1.5 space-y-1.5 pl-5 pr-1 text-[11px] text-[var(--text-secondary)]">
          <Show when={props.item.description}>
            <p class="whitespace-pre-wrap leading-relaxed">{props.item.description}</p>
          </Show>
          <Show when={props.item.steps}>
            <p class="whitespace-pre-wrap rounded-lg bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-muted)]">
              {props.item.steps}
            </p>
          </Show>
          <Show when={latestTesterFollowup()}>
            <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1">
              <p class="whitespace-pre-wrap text-[var(--text-primary)]">
                {latestTesterFollowup()!.text}
              </p>
            </div>
          </Show>
          <Show when={(props.item.attachments ?? []).length > 0}>
            <div class="flex flex-wrap gap-1.5 pt-0.5">
              <For each={props.item.attachments ?? []}>
                {(att) => (
                  <Show
                    when={att.mime.startsWith("image/") && att.url}
                    fallback={
                      <a
                        href={att.url}
                        target="_blank"
                        rel="noreferrer"
                        class="rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 font-mono text-[10px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)]"
                      >
                        {att.filename}
                      </a>
                    }
                  >
                    <button
                      type="button"
                      onClick={() =>
                        openImagePreview({ src: att.url!, alt: att.filename })
                      }
                      class="overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] cursor-pointer"
                    >
                      <img
                        src={att.url}
                        alt={att.filename}
                        class="h-12 w-16 object-cover"
                      />
                    </button>
                  </Show>
                )}
              </For>
            </div>
          </Show>

          <div class="flex flex-wrap items-center gap-1.5 pt-1">
            <Show when={canApprove()}>
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onApprove()}
                class="inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
              >
                <Play size={11} />
                <span>{t("tasks.intakeApproveBtn")}</span>
              </button>
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onSetStatus("rejected")}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--danger-text)] disabled:opacity-40 cursor-pointer"
              >
                <span>{t("tasks.intakeRejectBtn")}</span>
              </button>
            </Show>

            <Show when={props.item.status === "running"}>
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onSetStatus("cancel")}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--danger-text)] cursor-pointer"
              >
                <Square size={10} />
                <span>{t("tasks.intakeCancelRunBtn")}</span>
              </button>
            </Show>

            <Show when={props.item.status === "succeeded"}>
              <button
                type="button"
                disabled={busy()}
                onClick={() => setNoteOpen((v) => !v)}
                class="inline-flex items-center gap-1 rounded-lg bg-[var(--ok-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--ok-text)] cursor-pointer"
              >
                <Send size={11} />
                <span>{t("tasks.intakeMarkRetestBtn")}</span>
              </button>
            </Show>

            <Show
              when={
                props.item.status === "succeeded" ||
                props.item.status === "awaiting_retest"
              }
            >
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onSetStatus("resolved")}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <span>{t("tasks.intakeResolveBtn")}</span>
              </button>
            </Show>
          </div>

          <Show when={noteOpen()}>
            <div class="flex items-center gap-1.5 pt-1">
              <input
                type="text"
                value={devNote()}
                onInput={(e) => setDevNote(e.currentTarget.value)}
                placeholder={t("tasks.intakeDevNotePlaceholder")}
                class="min-w-0 flex-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
              />
              <button
                type="button"
                disabled={busy()}
                onClick={() => void onSetStatus("awaiting_retest", devNote())}
                class="rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] cursor-pointer"
              >
                {t("tasks.intakeConfirmBtn")}
              </button>
            </div>
          </Show>

          <Show when={error()}>
            <p class="text-[11px] text-[var(--danger-text)]">{error()}</p>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export default function IntakeTasksSection() {
  const [batchBusy, setBatchBusy] = createSignal(false);
  const pendingIDs = createMemo(() =>
    intakeTasks()
      .filter((item) => item.status === "staged" || item.status === "reopened")
      .map((item) => item.id)
  );

  const onBatchApprove = async () => {
    const ids = pendingIDs();
    if (ids.length === 0 || batchBusy()) return;
    setBatchBusy(true);
    try {
      await approveIntakeTasksBatch(ids);
    } finally {
      setBatchBusy(false);
    }
  };

  return (
    <Show when={intakeTasks().length > 0}>
      <section class="mb-3">
        <div class="mb-1.5 flex items-center justify-between gap-1.5 px-1">
          <div class="flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
            <ClipboardCheck size={12} class="text-[var(--accent-primary)]" />
            <span>{t("tasks.groupIntake")}</span>
            <span class="rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px]">
              {intakeTasks().length}
            </span>
          </div>
          <Show when={pendingIDs().length >= 2}>
            <button
              type="button"
              disabled={batchBusy()}
              onClick={() => void onBatchApprove()}
              class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-surface)] px-2 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
            >
              <Play size={10} />
              <span>{t("tasks.intakeBatchApproveBtn", { count: String(pendingIDs().length) })}</span>
            </button>
          </Show>
        </div>
        <For each={intakeTasks()}>
          {(item) => <IntakeTaskCard item={item} />}
        </For>
      </section>
    </Show>
  );
}
