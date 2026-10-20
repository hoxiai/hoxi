// web/src/i18n/locales/zh-CN/approval.ts
import type { ApprovalI18n } from "../../types";

export const approval: ApprovalI18n = {
  title: "安全确认",
  warning:
    "请求包含高权限或破坏性操作，请确认后继续",
  allowOnce: "仅允许一次",
  allowAlways: "本会话始终允许",
  reject: "拒绝",
  rejectWithFeedback: "拒绝并附上说明",
  rejectFeedbackPlaceholder:
    "可选：告诉 AI 换一种方式处理 (Enter 拒绝)…",
  command: "待执行命令",
  path: "目标路径",
  questionTitle: "请确认以下细节",
  skipQuestionTooltip: "跳过提问",
  multiSelect: "可多选",
  customAnswerLabel: "输入其他回答…",
  customAnswerPlaceholder: "输入补充说明或其他方案 (Enter 提交)…",
  confirmHint: "选择后点击确认继续",
  skipQuestion: "跳过",
  submitAnswer: "提交回答",
  openDiffInInspector: "在变更面板中查看",
  autoApproveRules: "放行规则 ({count})",
  ruleAlwaysAllow: "始终允许",
  ruleAlwaysDeny: "始终拒绝",
  modeMenuTitle: "确认模式",
  modeMenuBadge: "高危始终拦截",
  modeAutoLabel: "自动确认",
  modeAutoShort: "自动确认",
  modeAutoBadge: "推荐",
  modeAutoDesc:
    "自动放行编辑与普通命令；破坏性命令始终确认",
  modeSmartLabel: "智能确认",
  modeSmartShort: "智能确认",
  modeSmartDesc:
    "自动放行编辑与安全命令；其他 Shell 命令需确认",
  modeManualLabel: "手动确认",
  modeManualShort: "手动确认",
  modeManualDesc:
    "每次编辑、Shell 命令或外部操作前确认",
  modeTooltip: "确认模式：{mode}（点击切换）",
  openPolicySettings: "权限规则与 MCP…",
  highRiskBadge: "高危拦截",
  doomLoopNotice: "{tool} 连续多次使用相同参数，可能陷入循环。是否继续？",
  switchToAutoApprove: "切换为自动确认",
};
