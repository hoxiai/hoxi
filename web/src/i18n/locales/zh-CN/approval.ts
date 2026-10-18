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
  openDiffInInspector: "在右侧 Diff 检视器中查看",
  autoApproveRules: "细粒度放行规则 ({count})",
  ruleAlwaysAllow: "总是允许",
  ruleAlwaysDeny: "总是拒绝",
  modeMenuTitle: "确认模式",
  modeMenuBadge: "高危始终拦截",
  modeAutoLabel: "自动确认",
  modeAutoShort: "自动确认",
  modeAutoBadge: "推荐",
  modeAutoDesc:
    "自动放行文件读写与常规命令，仅高危破坏性指令（rm / sudo / --force 等）需手动确认",
  modeSmartLabel: "智能确认",
  modeSmartShort: "智能确认",
  modeSmartDesc:
    "自动放行文件修改与只读/构建测试命令，其余终端命令需手动确认",
  modeManualLabel: "手动确认",
  modeManualShort: "手动确认",
  modeManualDesc:
    "所有文件修改、终端命令与网络请求执行前均需手动点击确认",
  modeTooltip: "执行确认模式：{mode}（点击切换）",
  openPolicySettings: "细粒度权限与 MCP 策略…",
  highRiskBadge: "高危操作拦截",
  switchToAutoApprove: "切换为自动确认",
};
