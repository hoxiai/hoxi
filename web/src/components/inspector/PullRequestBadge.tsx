import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { ExternalLink, GitPullRequest, GitPullRequestArrow } from "lucide-solid";
import {
  fetchPullRequest,
  type ForgePullRequest,
} from "../../services/forge";
import { openExternalURL } from "../../store/workspace";
import { t } from "../../i18n";

type Tone = "ok" | "warn" | "danger" | "muted" | "accent";

const TONE_CLASS: Record<Tone, string> = {
  ok: "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]",
  warn: "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]",
  danger: "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]",
  muted: "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]",
  accent: "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]",
};

function stateLabel(pr: ForgePullRequest): string {
  if (pr.state === "OPEN" && pr.isDraft) return t("forge.states.draft");
  switch (pr.state) {
    case "OPEN":
      return t("forge.states.open");
    case "MERGED":
      return t("forge.states.merged");
    case "CLOSED":
      return t("forge.states.closed");
    default:
      return t("forge.states.open");
  }
}

function reviewLabel(pr: ForgePullRequest): string {
  switch (pr.reviewDecision) {
    case "APPROVED":
      return t("forge.review.approved");
    case "CHANGES_REQUESTED":
      return t("forge.review.changesRequested");
    case "REVIEW_REQUIRED":
      return t("forge.review.pending");
    default:
      return t("forge.review.none");
  }
}

function checksLabel(pr: ForgePullRequest): string {
  if (pr.checksTotal <= 0) return t("forge.checks.none");
  if (pr.checksFailed > 0) return t("forge.checks.failed", { count: pr.checksFailed });
  if (pr.checksPending > 0) return t("forge.checks.pending", { count: pr.checksPending });
  return t("forge.checks.passed", { passed: pr.checksPassed, total: pr.checksTotal });
}

function tone(pr: ForgePullRequest): Tone {
  if (pr.state === "MERGED") return "accent";
  if (pr.state === "CLOSED") return "danger";
  if (pr.state === "OPEN" && pr.isDraft) return "muted";
  if (pr.checksFailed > 0) return "danger";
  if (pr.reviewDecision === "CHANGES_REQUESTED") return "warn";
  if (pr.checksPending > 0) return "warn";
  return "ok";
}

export default function PullRequestBadge(props: {
  directory?: string;
  branch?: string;
  compareURL?: string;
  canCreate?: boolean;
  disabled?: boolean;
  compact?: boolean;
  onCreate?: () => void | Promise<void>;
}) {
  const [pr, setPr] = createSignal<ForgePullRequest | null>(null);
  const [loaded, setLoaded] = createSignal(false);
  let generation = 0;

  createEffect(() => {
    const directory = (props.directory ?? "").trim();
    const branch = (props.branch ?? "").trim();
    const current = ++generation;
    setPr(null);
    setLoaded(false);
    if (!directory) return;

    let disposed = false;
    const load = async (force: boolean) => {
      try {
        const response = await fetchPullRequest(directory, branch, force);
        if (disposed || current !== generation) return;
        setPr(response.available && response.pullRequest ? response.pullRequest : null);
      } catch {
        if (!disposed && current === generation) setPr(null);
      } finally {
        if (!disposed && current === generation) setLoaded(true);
      }
    };

    void load(false);
    const timer = window.setInterval(() => void load(true), 60_000);
    onCleanup(() => {
      disposed = true;
      window.clearInterval(timer);
    });
  });

  const activate = () => {
    const current = pr();
    if (current) {
      openExternalURL(current.url);
      return;
    }
    if (props.onCreate) {
      void props.onCreate();
      return;
    }
    if (props.compareURL) openExternalURL(props.compareURL);
  };

  const sizeClass = () => (props.compact ? "px-1.5 py-0.5" : "px-2 py-1");

  return (
    <Show
      when={pr()}
      fallback={
        <Show when={loaded() && props.canCreate && props.compareURL}>
          <button
            type="button"
            disabled={props.disabled}
            onClick={activate}
            title={t("inspector.diffs.createPrTooltip")}
            class={`hoxi-press shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer ${sizeClass()}`}
          >
            <GitPullRequestArrow size={10} />
            <span>{t("inspector.diffs.createPrBtn")}</span>
            <ExternalLink size={8} />
          </button>
        </Show>
      }
    >
      {(current) => (
        <button
          type="button"
          disabled={props.disabled}
          onClick={activate}
          aria-label={t("forge.open", { number: current().number })}
          title={t("forge.tooltip", {
            title: current().title,
            state: stateLabel(current()),
            checks: checksLabel(current()),
            review: reviewLabel(current()),
          })}
          class={`hoxi-press shrink-0 inline-flex items-center gap-1 rounded-md border text-[11px] font-mono font-medium hover:opacity-90 disabled:opacity-40 cursor-pointer ${sizeClass()} ${TONE_CLASS[tone(current())]}`}
        >
          <GitPullRequest size={10} />
          <span>#{current().number}</span>
        </button>
      )}
    </Show>
  );
}
