import type { ForgePullRequest } from "../../services/forge";
import { t } from "../../i18n";

export type PullRequestTone = "ok" | "warn" | "danger" | "muted" | "accent";

export const PULL_REQUEST_TONE_CLASS: Record<PullRequestTone, string> = {
  ok: "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]",
  warn: "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]",
  danger: "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]",
  muted: "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]",
  accent: "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]",
};

export function pullRequestStateLabel(pr: ForgePullRequest): string {
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

export function pullRequestReviewLabel(pr: ForgePullRequest): string {
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

export function pullRequestChecksLabel(pr: ForgePullRequest): string {
  if (pr.checksTotal <= 0) return t("forge.checks.none");
  if (pr.checksFailed > 0) return t("forge.checks.failed", { count: pr.checksFailed });
  if (pr.checksPending > 0) return t("forge.checks.pending", { count: pr.checksPending });
  return t("forge.checks.passed", { passed: pr.checksPassed, total: pr.checksTotal });
}

export function pullRequestTone(pr: ForgePullRequest): PullRequestTone {
  if (pr.state === "MERGED") return "accent";
  if (pr.state === "CLOSED") return "danger";
  if (pr.state === "OPEN" && pr.isDraft) return "muted";
  if (pr.checksFailed > 0) return "danger";
  if (pr.reviewDecision === "CHANGES_REQUESTED") return "warn";
  if (pr.checksPending > 0) return "warn";
  return "ok";
}

export function pullRequestTooltip(pr: ForgePullRequest): string {
  return t("forge.tooltip", {
    title: pr.title,
    state: pullRequestStateLabel(pr),
    checks: pullRequestChecksLabel(pr),
    review: pullRequestReviewLabel(pr),
  });
}
