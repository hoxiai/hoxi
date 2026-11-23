// web/src/i18n/locales/zh-CN/companion.ts
import type { I18nDictionary } from "../../types";

export const companion: I18nDictionary["companion"] = {
  nameHoxiChan: "呼吸酱",
  nameNekoBot: "赛博黑猫",
  nameCustom: "自定义看板娘",
  levelBadge: "Lv.{level}",
  todayTokensLabel: "今日投喂 {tokens}",

  // Mood status badges
  badgeIdle: "待机摸鱼",
  badgeCoding: "专注编码中",
  badgeConvoy: "{count} 路并行赛马",
  badgeGateDanger: "高危指令拦截",
  badgeGateApproval: "待你审批 ({count})",
  badgeFailover: "自动熔断重连",
  badgeCelebrate: "本轮搞定",

  // State-reactive speech bubble lines
  lineGateDanger: "危险动作急停！安全门控已拦下高危指令 [{target}]，点我立即核查",
  lineGateApproval: "有 {count} 项操作或提问正在等你拍板，点我一键切过去处理",
  lineFailover: "检测到上游波动（第 {attempt} 次重试），已稳住上下文准备自动切流",
  lineConvoy: "{count} 个隔离 Worktree 正在同台狂奔！先喝口水，写完在右侧对比挑最优解",
  lineCoding: "正在流式改码与巡检工具输出，思路偏了随时在下方输入框插话纠偏",
  lineCelebrate: "这轮干净收工啦！改动已同步到右侧 Diff 面板，随时检阅或一键回退",

  // Time-of-day & scheme-aware idle greetings
  idleMorning: "早安！本地多引擎已就绪，今天是开个新 Worktree 还是接管昨天的 CLI 会话？",
  idleAfternoon: "下午好，30MB 原生内存稳稳在线，随时准备啃最硬的重构骨头",
  idleEvening: "晚上好！拿不准哪个模型写得更漂亮？拉两个 Worktree 赛马比一比吧",
  idleLateNight: "夜深啦，杂活交给 Seatbelt 沙盒跑，老兵注意护肝早点休息",
  idleObsidian: "耀石黑金痛机模式已点亮，暗夜流金高对比，免登录纯本地直连开干",

  // Interactive poke lines
  poke1: "别戳啦在盯 Diff 呢……不过切到毛玻璃痛机皮肤，重构多巴胺确实拉满",
  poke2: "小技巧：本机 Claude Code、Codex、Gemini 跑一半限流了，点左栏能无损接管继续写",
  poke3: "开着全局代理怕 502？Go 回环层已经强制绕过系统代理啦，稳得很",
  poke4: "看似毫无用处的桌面挂件，实乃深夜改 Bug 必不可少的赛博电子布洛芬",

  // Welcome Stage quick actions & labels
  actionHandoff: "接管外部 CLI 会话",
  actionSwitchWorktree: "开启 Worktree 沙盒",
  actionWorktreeActive: "Worktree 沙盒已开启",
  actionPoke: "戳一戳换台词",
  actionJumpApproval: "前往审批 →",
  minimizeTooltip: "收起桌宠挂件",
  expandTooltip: "展开桌宠挂件",

  // Settings & Header controls
  headerQuickToggle: "二次元看板娘",
  sectionTitle: "二次元看板娘与欢迎舞台",
  sectionDesc: "将原创看板娘、赛博黑猫或自定义立绘绑定至实时编码流、Worktree 赛马与高危命令拦截门控",
  enableLabel: "开启工作区看板娘 / 桌宠",
  enableDesc: "实时响应会话思考、多路并行、502 故障切流与高危命令硬拦截提醒",
  personaLabel: "伴侣形象选择",
  personaHoxiChan: "呼吸酱（二次元少女）",
  personaNekoBot: "Neko-01（赛博黑猫）",
  personaCustom: "自定义立绘",
  welcomeStageLabel: "空会话立绘欢迎舞台",
  welcomeStageDesc: "在新建空白会话页展示角色欢迎卡、今日 Token 等级与快捷入口",
  cornerDockLabel: "桌面窗外悬浮桌宠",
  cornerDockDesc: "在软件窗口外独立置顶悬浮（支持自由拖拽、空白穿透与右键菜单；网页端自动回退为角落浮窗）",
  resetPositionBtn: "重置屏幕位置",
  speechBubbleLabel: "状态联动台词气泡",
  speechBubbleDesc: "拦截高危指令、触发上游重试或完成任务时弹出角色互动气泡",
  customStandeeTitle: "导入自定义角色立绘 / 动图",
  customStandeeDesc: "支持透明背景 PNG、WebP 或动态 GIF（上限 15 MB，IndexedDB 本地离线保存）",
  uploadStandeeBtn: "上传立绘 / 动图…",
  clearStandeeBtn: "移除自定义立绘",
  customStandeeActive: "已加载本地立绘",
  errInvalidType: "请选择 PNG、WebP、GIF 或 JPG 格式图片",
  errTooLarge: "立绘文件大小超过 15 MB 上限",
  errStorage: "无法写入本地浏览器数据库",
};
