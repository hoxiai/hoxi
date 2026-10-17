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
};
