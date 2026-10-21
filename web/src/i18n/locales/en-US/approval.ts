// web/src/i18n/locales/en-US/approval.ts

export const approval = {
  title: "Approval Required",
  warning:
    "Privileged or destructive operation requested. Confirm to proceed",
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
  ruleScopeHint:
    "Picking a rule also answers this request. Exact rules are saved to the global permission config; rules with * apply to this session only. Destructive requests can only be denied",
  modeMenuTitle: "Approval Mode",
  modeMenuBadge: "Destructive Gated",
  modeAutoLabel: "Auto Approve",
  modeAutoShort: "Auto",
  modeAutoBadge: "Default",
  modeAutoDesc:
    "Auto-approves edits and normal commands; destructive commands always ask",
  modeSmartLabel: "Smart Ask",
  modeSmartShort: "Smart",
  modeSmartDesc:
    "Auto-approves edits and safe commands; other shell commands ask",
  modeManualLabel: "Manual Ask",
  modeManualShort: "Manual",
  modeManualDesc:
    "Asks before every edit, shell command and external action",
  modeTooltip: "Approval mode: {mode} (click to switch)",
  openPolicySettings: "Permission Rules & MCP…",
  highRiskBadge: "High Risk",
  doomLoopNotice:
    "{tool} was called repeatedly with identical input; it may be stuck. Continue?",
  switchToAutoApprove: "Switch to Auto Approve",
  allowSessionCommand: "Always Allow Command (Session)",
  allowSessionToolCall: "Always Allow Exact Call (Session)",
  sessionGrantScopeCommand:
    "Remembers this exact command in memory for the current session only; cleared on restart and revocable in Settings",
  sessionGrantScopeTool:
    "Remembers this tool + exact arguments in memory for the current session only; revocable in Settings",
  expiresIn: "Auto-rejects in {seconds}s",
  toolArgsLabel: "Tool Arguments",
  grantsSectionTitle: "Active Session Grants (In-Memory)",
  grantsSectionDesc:
    "Created when you choose Always Allow in a session. Stored in memory only (cleared on restart) and never covers destructive commands",
  grantsEmpty: "No active session grants",
  revokeGrant: "Revoke",
  revokeAllGrants: "Revoke All ({count})",
  auditSectionTitle: "Approval Audit Log",
  auditSectionDesc:
    "Persistent record of every desktop, remote tunnel, or timed-out approval decision (~/.hoxi/approval-audit.json)",
  auditEmpty: "No approval decisions recorded yet",
  auditRefresh: "Refresh",
  auditClear: "Clear Log",
  sourceDesktop: "Desktop",
  sourceTunnel: "Remote Tunnel",
  sourceSystem: "System",
  decisionOnce: "Allow Once",
  decisionAlways: "Session Always",
  decisionReject: "Rejected",
  decisionTimeout: "Timed Out",
  decisionInterrupted: "Interrupted",
};
