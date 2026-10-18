// web/src/i18n/locales/en-US/approval.ts

export const approval = {
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
  modeMenuTitle: "Approval Mode",
  modeMenuBadge: "Destructive Gated",
  modeAutoLabel: "Auto Approve",
  modeAutoShort: "Auto",
  modeAutoBadge: "Default",
  modeAutoDesc:
    "Auto-approves file edits and normal commands; destructive commands (rm / sudo / --force) always require confirmation",
  modeSmartLabel: "Smart Ask",
  modeSmartShort: "Smart",
  modeSmartDesc:
    "Auto-approves file edits and safe read/build/test commands; asks before other shell commands",
  modeManualLabel: "Manual Ask",
  modeManualShort: "Manual",
  modeManualDesc:
    "Requires manual confirmation before every file edit, shell command, or external action",
  modeTooltip: "Approval mode: {mode} (click to switch)",
  openPolicySettings: "Fine-grained Policy & MCP…",
  highRiskBadge: "High-Risk Gate",
  switchToAutoApprove: "Switch to Auto Approve",
};
