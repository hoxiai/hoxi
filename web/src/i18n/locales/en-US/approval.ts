// web/src/i18n/locales/en-US/approval.ts
import type { ApprovalI18n } from "../../types";

export const approval: ApprovalI18n = {
  title: "Security Approval Gate",
  warning:
    "The agent requested a destructive or privileged command. Please review carefully.",
  allowOnce: "Allow Once",
  allowAlways: "Always Allow (Session)",
  reject: "Reject",
  rejectWithFeedback: "Reject & Send Feedback",
  rejectFeedbackPlaceholder:
    "Optional: tell Hoxi what to do differently (Enter to reject)…",
  command: "Command",
  path: "Target Path",
  questionTitle: "Agent Question",
  skipQuestionTooltip: "Skip / Dismiss",
  multiSelect: "Multi-select",
  customAnswerLabel: "Add custom answer…",
  customAnswerPlaceholder:
    "Type additional details or alternative (Enter to submit)…",
  confirmHint: "Select options and confirm to proceed",
  skipQuestion: "Skip",
  submitAnswer: "Submit Answer",
  openDiffInInspector: "Open in Diff Inspector",
  autoApproveRules: "Auto-approve rules ({count})",
  ruleAlwaysAllow: "Always Allow",
  ruleAlwaysDeny: "Always Deny",
};
