// web/src/i18n/locales/zh-CN/skills.ts
import type { SkillsI18n } from "../../types";

export const skills: SkillsI18n = {
  title: "技能",
  searchPlaceholder: "按名称或说明筛选技能…",
  refresh: "刷新目录",
  refreshing: "刷新中…",
  cacheNotice:
    "刷新仅同步引擎目录；磁盘新增文件可能需要重载工作区",
  permissionNotice:
    "可用性取决于 Agent 权限与运行时安全门控",
  hideNotice:
    "隐藏仅移出 Hoxi 列表与 `/` 建议，不会阻止模型自动调用",
  showHidden: "显示已隐藏 ({count})",
  hideHidden: "收起已隐藏",
  hideFromList: "从 Hoxi 列表隐藏",
  unhideFromList: "恢复显示",
  hiddenBadge: "已隐藏",
  sourceBuiltin: "内置",
  sourceProject: "项目",
  sourceUser: "个人",
  sourceExternal: "外部",
  builtinInMemory: "内置技能（内存内容）",
  openFile: "在文件中查看",
  revealInFinder: "在访达中显示",
  selectInComposer: "在输入框中使用",
  selectedInComposer: "已加入输入框",
  removeSelection: "移除所选技能",
  viewSkillDetail: "查看技能详情",
  openSkillsPanel: "浏览技能库",
  emptyCatalog: "当前目录未发现可用技能",
  emptyFiltered: "没有匹配的技能",
  unsupportedTitle: "当前运行时（{runtime}）暂未支持技能目录",
  unsupportedDesc:
    "原生技能目前支持 OpenCode 会话；切换到 OpenCode 模型后即可浏览与调用",
  selectToPreview: "选择技能以预览 SKILL.md 正文",
  loadingCatalog: "正在读取技能目录…",
  loadingDetail: "正在读取技能正文…",
  slashGroupSkills: "技能 (Skills)",
  slashGroupCommands: "命令 (Commands)",
  slashGroupMcp: "MCP 提示 (MCP)",
  slashUseSkillDetail: "选择该技能用于本条消息",
  composerSkillBadge: "技能",
  statusSelected: "已选择（待发送）",
  statusRequested: "已提交技能请求",
  statusLoading: "正在加载技能…",
  statusLoaded: "已加载原生技能",
  statusError: "技能加载失败",
  statusUnconfirmed: "本轮未确认加载",
  reselectRequired:
    "技能「{name}」在当前沙箱中已变更或不可用，请重新选择后再发送",
  queueSkillInvalid:
    "队列消息已暂停：技能「{name}」在当前目录已失效，请编辑或重新选择",
  digestLabel: "正文摘要",
  sourcePathLabel: "来源位置",
  browseSkills: "浏览技能库",
  groupSkills: "技能",
  groupMcp: "MCP 提示",
  groupCommands: "命令",
  composerPillPrefix: "技能：",
  removeFromComposer: "从输入框移除",
  sandboxSkillMissing:
    "技能「{name}」在新沙箱中不可用，已取消选择",
  runtimeSwitchedCleared:
    "已取消技能「{name}」，当前仅支持 OpenCode",
};
