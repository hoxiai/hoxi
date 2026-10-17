// web/src/i18n/locales/zh-CN/timeline.ts
import type { TimelineI18n } from "../../types";

export const timeline: TimelineI18n = {
  welcomeTitle: "原生 AI 智能体研发工作台",
  welcomeSubtitle:
    "基于 OpenCode 原生运行时、8 维结构化记忆与 Git 工作树强隔离。",
  welcomeQuestion: "在 {project} 中开启何种研发任务？",
  welcomePrompt1: "分析并梳理项目整体架构与设计模式",
  welcomePrompt2: "运行代码诊断并自愈编译与类型错误",
  welcomePrompt3: "创建隔离工作树并开启功能分支开发",
  thoughtFor: "深度思考用时 {time} 秒",
  thinking: "思考中…",
  thought: "深度思考",
  showThought: "展开思考过程",
  hideThought: "折叠思考过程",
  revertHere: "回滚到此处",
  forkHere: "从此处分支会话",
  revertConfirm: "确认将对话和工作区代码回滚至此检查点吗？",
  forkConfirm: "确认从该节点衍生出新会话分支吗？",
  steer: "实时纠偏",
  stop: "中止",
  send: "发送",
  buildMode: "构建模式",
  planMode: "规划模式",
  buildDesc: "拥有完整自主执行权限，可调用工具并修改文件",
  planDesc: "只读探索代码与规划架构，不产生实际文件改动",
  inputPlaceholder:
    "输入任务指令或问题... (Enter 发送，Shift+Enter 换行)",
  steerPlaceholder:
    "Agent 正在执行中。输入实时纠偏建议，无需中断进度...",
  filesAttached: "{count} 个文件",
  addContext: "添加上下文",
  selectModel: "选择模型",
  reads: "{count} 次读取",
  searches: "{count} 次搜索",
  lists: "{count} 次检索",
  thoughts: "{count} 次思考",
};
