// web/src/i18n/locales/en-US/approval.ts

export const approval = {
  title: "Approval Required",
  warning:
    "A privileged or potentially destructive operation was requested. Please confirm to proceed.",
  allowOnce: "Allow Once",
  allowAlways: "Always Allow (Session)",
  reject: "Reject",
  rejectWithFeedback: "Reject with Note",
  rejectFeedbackPlaceholder:
    "Optional: suggest a different approach (Enter to reject)…",
  command: "Command",
  path: "Target Path",
  questionTitle: "Confirm Details",
  skipQuestionTooltip: "Skip",
  multiSelect: "Multi-select",
  customAnswerLabel: "Other answer…",
  customAnswerPlaceholder:
    "Type additional details or an alternative (Enter to submit)…",
  confirmHint: "Select options and confirm to continue",
  skipQuestion: "Skip",
  submitAnswer: "Submit Answer",
  openDiffInInspector: "View in Diffs",
  autoApproveRules: "Approval rules ({count})",
  ruleAlwaysAllow: "Always Allow",
  ruleAlwaysDeny: "Always Deny",
  modeMenuTitle: "Approval Mode",
  modeMenuBadge: "Destructive Gated",
  modeAutoLabel: "Auto Approve",
  modeAutoShort: "Auto",
  modeAutoBadge: "Default",
  modeAutoDesc:
    "Auto-approves file edits and normal commands; destructive commands (rm / sudo / --force) always ask",
  modeSmartLabel: "Smart Ask",
  modeSmartShort: "Smart",
  modeSmartDesc:
    "Auto-approves file edits and safe read/build/test commands; asks before other shell commands",
  modeManualLabel: "Manual Ask",
  modeManualShort: "Manual",
  modeManualDesc:
    "Asks for confirmation before every file edit, shell command, or external action",
  modeTooltip: "Approval mode: {mode} (click to switch)",
  openPolicySettings: "Permission Rules & MCP…",
  highRiskBadge: "High Risk",
  switchToAutoApprove: "Switch to Auto Approve",
};
