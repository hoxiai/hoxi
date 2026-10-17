// web/src/i18n/locales/zh-CN/approval.ts
import type { ApprovalI18n } from "../../types";

export const approval: ApprovalI18n = {
  title: "安全执行门控",
  warning:
    "Agent 申请执行具有破坏性或高权限的操作，请谨慎确认。",
  allowOnce: "仅允许一次",
  allowAlways: "本会话始终允许",
  reject: "拒绝执行",
  rejectWithFeedback: "拒绝并发送建议",
  rejectFeedbackPlaceholder:
    "可选：告知 Agent 期望的不同执行方式 (Enter 拒绝)...",
  command: "待执行命令",
  path: "目标路径",
  questionTitle: "Agent 需要确认方案细节",
  skipQuestionTooltip: "跳过提问",
  multiSelect: "可多选",
  customAnswerLabel: "输入自定义回答...",
  customAnswerPlaceholder: "输入补充说明或其它方案 (Enter 提交)...",
  confirmHint: "选择后点击确认继续执行",
  skipQuestion: "跳过",
  submitAnswer: "提交回答",
};
