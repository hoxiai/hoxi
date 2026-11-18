// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for Chinese (`zh`).
class AppLocalizationsZh extends AppLocalizations {
  AppLocalizationsZh([String locale = 'zh']) : super(locale);

  @override
  String get appName => 'Hoxi';

  @override
  String get navChat => '沟通';

  @override
  String get commonCancel => '取消';

  @override
  String get commonRefresh => '刷新';

  @override
  String chatComposerSkillHint(String skill) {
    return '向 @$skill 发消息或按住说话…';
  }

  @override
  String get chatSend => '发送消息';

  @override
  String get chatHideKeyboard => '收起键盘';

  @override
  String get loginSubtitle => '连接 Hoxi 桌面端或 GoPanel 面板';

  @override
  String get loginNodeAddress => '连接地址';

  @override
  String get loginAddressHint =>
      'http://192.168.1.x:port 或 https://demo.gopanel.run';

  @override
  String get loginAddressRequired => '请输入连接地址';

  @override
  String get loginAddressScheme => '请输入带有 http/https 的完整地址';

  @override
  String get loginPassword => '密码';

  @override
  String get loginPasswordHint => '请输入密码（Hoxi 桌面端可留空）';

  @override
  String get loginPasswordRequired => '请输入密码';

  @override
  String get loginConnect => '连 接';

  @override
  String get loginScanQr => '扫描二维码授权';

  @override
  String get loginConnectFailed => '连接失败';

  @override
  String get engineeringAddNode => '添加连接';

  @override
  String get loginEntranceLabel => '安全入口（可选）';

  @override
  String get loginEntranceHint => '例如：your-entrance';

  @override
  String get loginEntranceInvalid => '只能包含字母、数字、下划线和短横线';

  @override
  String get loginAdminEmail => '管理员邮箱';

  @override
  String get loginAdminEmailHint => '请输入管理员邮箱';

  @override
  String chatPendingApprovals(int count) {
    return '$count 项待审批';
  }

  @override
  String get chatModelPickerTitle => '选择模型';

  @override
  String get chatTooltipHoldToTalk => '按住说话';

  @override
  String get chatTooltipCamera => '拍照';

  @override
  String get chatCameraUnavailable => '相机不可用。请检查相机权限；取消拍照不会发送图片。';

  @override
  String get chatListeningShort => '正在听…';

  @override
  String get settingsOverviewTitle => '我的';

  @override
  String get addNodeHoxiTitle => 'Hoxi 桌面端';

  @override
  String get addNodeHoxiSubtitle => '连接桌面端 Hoxi 工作台，协同 AI 编码与研发';

  @override
  String get addNodeGopanelTitle => 'GoPanel';

  @override
  String get addNodeGopanelSubtitle => '连接服务器面板，管理主机、容器、站点与证书';

  @override
  String get addNodeComingSoon => '待支持';

  @override
  String get loginHoxiTitle => '连接 Hoxi 桌面端';

  @override
  String get loginHoxiSubtitle => '扫描电脑端二维码，或填写局域网/中继地址';

  @override
  String get loginGopanelTitle => '连接 GoPanel 面板';

  @override
  String get loginGopanelSubtitle => '使用面板管理员账号登录';

  @override
  String get loginPanelAddress => '面板地址';

  @override
  String get loginPanelAddressHint => 'https://demo.gopanel.run';

  @override
  String get loginHoxiAddress => '电脑地址';

  @override
  String get loginHoxiAddressHint => 'http://192.168.x.x:5417 或中继地址';

  @override
  String get loginHoxiToken => '配对令牌（可选）';

  @override
  String get loginHoxiTokenHint => '若桌面端配置了配对令牌请在此输入，未开启可留空';

  @override
  String get loginHoxiQrHint => '在电脑端 Hoxi「设置 → 远程访问」中扫码直连';

  @override
  String get loginScanHoxiQr => '扫描电脑端二维码';

  @override
  String get loginScanPanelQr => '扫描面板二维码';

  @override
  String get loginUsername => '账号（可选）';

  @override
  String get loginPasswordHintPanel => '请输入管理员密码';

  @override
  String get settingsDeviceRenameHint => '给这个连接起个好认的名字';

  @override
  String get commonSave => '保存';

  @override
  String get qrErrorMissingUrl => '二维码格式不正确，缺少设备地址';

  @override
  String get qrErrorMissingToken => '二维码格式不正确，缺少面板地址或凭证';

  @override
  String get qrErrorWrongKindPanel => '这是面板二维码，请返回选择「GoPanel 面板」';

  @override
  String get qrErrorWrongKindHoxi => '这是电脑二维码，请返回选择「Hoxi 桌面端」';

  @override
  String get qrErrorVerifyFailed => '连接验证失败';

  @override
  String get qrErrorUnrecognized => '无法识别的二维码内容';

  @override
  String get qrErrorRelayMissing => '二维码格式不正确，缺少面板地址或节点';

  @override
  String get qrErrorRelaySignInFirst => '请先在「服务器列表」登录该 GoPanel 面板后再扫码';

  @override
  String get qrErrorRelayConnectFailed => '无法经 GoPanel 连接该电脑';

  @override
  String get qrAlignHint => '请将面板端生成的二维码对准框内';

  @override
  String get qrVerifying => '正在验证并连接服务器...';

  @override
  String get serverListUnreachable => '无法连接该设备，请确认设备在线且网络可达';

  @override
  String serverListRelayConnectFailed(String name) {
    return '无法经 GoPanel 连接 $name，请确认电脑在线且 GoPanel 登录有效';
  }

  @override
  String get relayNodesTitle => '经此 GoPanel 连接的 Hoxi 桌面电脑';

  @override
  String get relayNodesSubtitle =>
      '在电脑端 Hoxi 设置 → 经 GoPanel 远程控制 中填入 GoPanel 生成的配对码后，这里会出现该电脑';

  @override
  String relayNodesLoadFailed(String error) {
    return '读取失败：$error';
  }

  @override
  String get relayNodesEmpty => '暂无已配对的 Hoxi 桌面电脑';

  @override
  String get relayNodeOnline => '在线';

  @override
  String get relayNodeOffline => '离线';

  @override
  String get commonClose => '关闭';

  @override
  String get containerFilterAll => '全部';

  @override
  String get nodeLinkDevicesSection => '连接对象';

  @override
  String get codeWsFilesTitle => '项目文件';

  @override
  String get codeWsFilesRoot => '项目根目录';

  @override
  String get codeWsFilesEmpty => '空目录';

  @override
  String get codeWsFilesTruncated => '仅显示前 500 项';

  @override
  String get codeWsFilesEditing => '正在编辑';

  @override
  String get codeWsFilesSaved => '已保存';

  @override
  String get codeWsFilesUnsaved => '未保存';

  @override
  String get codeWsFilesSave => '保存';

  @override
  String get codeWsFilesSaveSuccess => '已保存';

  @override
  String get codeWsFilesUnsavedTitle => '放弃修改？';

  @override
  String get codeWsFilesUnsavedHint => '返回后修改将丢失';

  @override
  String get codeWsFilesDiscard => '放弃修改';

  @override
  String get codeWsFilesKeepEditing => '继续';

  @override
  String get codeWsFilesCopy => '复制内容';

  @override
  String get codeWsFilesCopied => '已复制';

  @override
  String get codeWsFilesOperationFailed => '操作失败';

  @override
  String get codeWsActionBack => '返回';

  @override
  String get codeWsActionParent => '上一级';

  @override
  String get codeWsActionRefresh => '刷新';

  @override
  String get codeWsActionRetry => '重试';

  @override
  String get codeWsActionStop => '停止';

  @override
  String get codeWsActionDismiss => '关闭';

  @override
  String get codeWsChatTitle => 'Hoxi 开发';

  @override
  String get codeWsChatStreamReconnecting => '重连中…';

  @override
  String get codeWsChatRefresh => '刷新状态';

  @override
  String get codeWsChatGitReview => '变更审查';

  @override
  String get codeWsChatChooseSession => '选择会话';

  @override
  String get codeWsChatPreviewTitle => '预览';

  @override
  String get codeWsChatEmptyTitle => '暂无会话';

  @override
  String get codeWsChatEmptyHint => '新建会话后在这里发指令';

  @override
  String codeWsChatSessionFallback(int id) {
    return '开发 #$id';
  }

  @override
  String get codeWsSessionSettingsTitle => '会话设置';

  @override
  String get codeWsSessionSettingsName => '会话名称';

  @override
  String get codeWsSessionSettingsApproval => '审批策略';

  @override
  String get codeWsSessionSettingsPolicyManual => '每次确认';

  @override
  String get codeWsSessionSettingsPolicySafeAuto => '安全自动';

  @override
  String get codeWsSessionSettingsPolicyFullAuto => '全自动';

  @override
  String codeWsSessionSettingsPolicyRejectsInConversation(String executor) {
    return '$executor 在对话模式会直接拒绝需授权的操作，逐项确认请用终端模式。';
  }

  @override
  String get codeWsSessionSettingsModel => '模型';

  @override
  String get codeWsSessionSettingsModelHint => '留空用默认模型';

  @override
  String get codeWsSessionSettingsEffort => '推理强度';

  @override
  String get codeWsSessionSettingsEffortDefault => '默认';

  @override
  String get codeWsSessionSettingsSave => '保存设置';

  @override
  String get codeWsSessionSettingsSaved => '已保存';

  @override
  String get codeWsAttentionTitle => '待我处理';

  @override
  String get codeWsAttentionViewAll => '全部';

  @override
  String get codeWsAttentionEmpty => '暂无待处理';

  @override
  String get codeWsAttentionLoadFailed => '加载失败';

  @override
  String codeWsAttentionSession(int id) {
    return '开发会话 #$id';
  }

  @override
  String get codeWsAttentionConfirmHint => '确认执行？';

  @override
  String get codeWsAttentionCancel => '取消';

  @override
  String get codeWsAttentionConfirm => '确认';

  @override
  String get codeWsAttentionActionSuccess => '已完成';

  @override
  String get codeWsAttentionActionFailed => '操作失败';

  @override
  String get codeWsAttentionOpenFailed => '打开失败';

  @override
  String get codeWsInstructionClosedHint => '会话已交付';

  @override
  String get codeWsInstructionInputHint => '输入指令';

  @override
  String get codeWsInstructionSelectSessionHint => '先选择会话';

  @override
  String get codeWsInstructionSend => '发送';

  @override
  String get codeWsHubTitle => '开发';

  @override
  String get codeWsHubRecentTasks => '最近任务';

  @override
  String get codeWsHubProjectsTab => '项目';

  @override
  String codeWsHubSessionCount(int count) {
    return '$count 个';
  }

  @override
  String get codeWsHubProject => '当前项目';

  @override
  String get codeWsHubNoProjectTasks => '暂无任务';

  @override
  String get codeWsHubNoProjectTasksHint => '新建会话后显示任务';

  @override
  String get codeWsHubCreate => '新建';

  @override
  String get codeWsHubProjectTerminal => '项目终端';

  @override
  String get codeWsHubProjectTerminalHint => '进入项目目录执行命令';

  @override
  String get codeWsTerminalChooseProject => '选择项目';

  @override
  String get codeWsTerminalChooseProjectHint => '在项目目录中启动';

  @override
  String get codeWsTerminalNoProjects => '暂无项目';

  @override
  String get codeWsTerminalOpenFailed => '打开失败';

  @override
  String get codeWsTerminalOpening => '打开中';

  @override
  String get codeWsTerminalProjectTitle => '项目终端';

  @override
  String get codeWsTerminalNoTask => '暂无任务';

  @override
  String codeWsTerminalTaskTitle(String task) {
    return '任务 · $task';
  }

  @override
  String get codeWsTerminalReconnect => '重新连接';

  @override
  String get codeWsTerminalConnecting => '连接中…';

  @override
  String get codeWsTerminalReconnecting => '重连中…';

  @override
  String get codeWsTerminalConnectionFailed => '连接失败';

  @override
  String get codeWsTerminalSessionClosed => '会话已结束';

  @override
  String get codeWsTerminalRelayUnsupported => '隧道不支持终端，请改用局域网直连';

  @override
  String get codeWsTerminalConnected => '已连接';

  @override
  String get codeWsTerminalReleaseControl => '释放';

  @override
  String get codeWsTerminalTakeControl => '接管';

  @override
  String get codeWsTerminalSessionStatus => '会话';

  @override
  String get codeWsTerminalFiles => '文件';

  @override
  String get codeWsTerminalCommandHint => '输入命令';

  @override
  String get codeWsTerminalDisconnected => '未连接';

  @override
  String get codeWsTerminalTakeControlHint => '点输入框接管';

  @override
  String get codeWsTerminalSendCommand => '发送命令';

  @override
  String get codeWsTerminalHistoryPrevious => '↑历史';

  @override
  String get codeWsTerminalHistoryNext => '↓历史';

  @override
  String get codeWsTerminalPaste => '粘贴';

  @override
  String get codeWsTerminalNativeKeyboard => '键盘';

  @override
  String get codeWsSessionIsolateLabel => '隔离工作区';

  @override
  String get codeWsSessionIsolateNo => '否';

  @override
  String get codeWsSessionIsolateYes => '是';

  @override
  String get codeWsSessionWorktreeChecking => '检查工作区…';

  @override
  String get codeWsSessionWorktreeReady => '隔离工作区';

  @override
  String get codeWsSessionWorktreeReadyHint => '在独立 Worktree 中运行';

  @override
  String get codeWsSessionWorktreeDirty => '项目存在未提交变更';

  @override
  String codeWsSessionWorktreeDirtyHint(String repositories) {
    return '先在桌面端处理：$repositories';
  }

  @override
  String get codeWsSessionWorktreeUnavailable => '无法创建会话';

  @override
  String get codeWsSessionWorktreeReasonNotGit => '项目不是 Git 仓库';

  @override
  String get codeWsSessionWorktreeReasonSourceUnavailable => '源码目录不可用';

  @override
  String get codeWsSessionWorktreeReasonUnknown => '不支持 Worktree';

  @override
  String get codeWsSessionWorktreeCheckFailed => '检查失败';

  @override
  String get codeWsSessionWorktreeRetry => '重新检查';

  @override
  String get codeWsStopTitle => '停止当前开发任务？';

  @override
  String get codeWsStopDescription => '执行中与排队指令会停止，改动保留';

  @override
  String get codeWsStopCancel => '继续执行';

  @override
  String get codeWsStopConfirm => '确认停止';

  @override
  String get codeWsStopSuccess => '已发送停止请求';

  @override
  String get codeWsRecoveryTitle => '会话恢复';

  @override
  String get codeWsRecoveryInitialization => '会话初始化';

  @override
  String get codeWsRecoveryInitializationFailed => '初始化失败';

  @override
  String get codeWsRecoveryInitializationFailedHint => '环境初始化失败';

  @override
  String get codeWsRecoveryInitializing => '初始化中';

  @override
  String get codeWsRecoveryInitialized => '已初始化';

  @override
  String get codeWsRecoveryInitializingHint => '准备环境中';

  @override
  String get codeWsRecoveryInitializedHint => '可继续发送指令';

  @override
  String get codeWsRecoveryRetryInitialization => '重新初始化';

  @override
  String get codeWsRecoveryRetryInitializationHint => '重新准备环境？';

  @override
  String get codeWsRecoveryConversation => '完整对话';

  @override
  String get codeWsRecoveryNoMessages => '暂无消息';

  @override
  String get codeWsRecoveryRuns => '执行记录';

  @override
  String get codeWsRecoveryNoRuns => '暂无记录';

  @override
  String get codeWsRecoveryViewRunDetail => '查看详情';

  @override
  String get codeWsRecoveryLoadMore => '加载更多';

  @override
  String get codeWsRecoveryUser => '你';

  @override
  String get codeWsRecoveryAgent => 'Code 助手';

  @override
  String codeWsRecoveryDuration(int duration) {
    return '耗时 $duration ms';
  }

  @override
  String codeWsRecoveryTokens(int tokens) {
    return '$tokens tokens';
  }

  @override
  String get codeWsRecoveryRetryInstruction => '重试指令';

  @override
  String get codeWsRecoveryRetryInstructionHint => '重新执行该指令？';

  @override
  String get codeWsRecoveryRetryQueued => '已重新加入队列';

  @override
  String get codeWsRecoveryStatusCompleted => '已完成';

  @override
  String get codeWsRecoveryStatusFailed => '失败';

  @override
  String get codeWsRecoveryStatusStopped => '已停止';

  @override
  String get codeWsRecoveryStatusRunning => '执行中';

  @override
  String get codeWsRecoveryStatusAwaitingApproval => '等待审批';

  @override
  String get codeWsRecoveryStatusQueued => '排队中';

  @override
  String get codeWsRecoveryStatusUnknown => '未知状态';

  @override
  String get codeWsRecoveryLoadFailed => '会话历史加载失败';

  @override
  String get codeWsRecoveryCancel => '取消';

  @override
  String get codeWsRecoveryConfirm => '确认';

  @override
  String get codeWsRecoveryActionSuccess => '操作已提交';

  @override
  String get codeWsRecoveryActionFailed => '操作失败，请稍后重试';

  @override
  String codeWsRunDetailTitle(int id) {
    return '运行 #$id';
  }

  @override
  String get codeWsRunDetailCopy => '复制诊断信息';

  @override
  String get codeWsRunDetailCopied => '诊断信息已复制';

  @override
  String get codeWsRunDetailLoadFailed => '运行详情加载失败';

  @override
  String get codeWsRunDetailEmpty => '没有可展示的运行详情';

  @override
  String get codeWsRunDetailOverview => '运行概览';

  @override
  String get codeWsRunDetailStatus => '状态';

  @override
  String get codeWsRunDetailExecutor => '执行器';

  @override
  String get codeWsRunDetailModel => '模型';

  @override
  String get codeWsRunDetailExitCode => '退出码';

  @override
  String get codeWsRunDetailDurationLabel => '耗时';

  @override
  String get codeWsRunDetailSession => '会话';

  @override
  String get codeWsRunDetailTask => '任务';

  @override
  String get codeWsRunDetailInstruction => '指令';

  @override
  String get codeWsRunDetailStartedAt => '开始时间';

  @override
  String get codeWsRunDetailCompletedAt => '结束时间';

  @override
  String get codeWsRunDetailInputTokens => '输入 Tokens';

  @override
  String get codeWsRunDetailOutputTokens => '输出 Tokens';

  @override
  String get codeWsRunDetailCachedTokens => '缓存输入 Tokens';

  @override
  String get codeWsRunDetailReasoningTokens => '推理 Tokens';

  @override
  String get codeWsRunDetailTotalTokens => '总 Tokens';

  @override
  String get codeWsRunDetailPrompt => '原始指令';

  @override
  String get codeWsRunDetailError => '错误详情';

  @override
  String get codeWsRunDetailOutput => '执行输出';

  @override
  String get codeWsRunDetailRawOutput => '原始日志';

  @override
  String get codeWsRunDetailNoPrompt => '没有记录原始指令';

  @override
  String get codeWsRunDetailNoOutput => '没有结构化执行输出';

  @override
  String get codeWsRunDetailNoRawOutput => '没有可用的原始日志';

  @override
  String get codeWsDeliveryTitle => '统一交付';

  @override
  String get codeWsDeliveryEmpty => '将当前会话变更合并到项目主分支并统一推送。';

  @override
  String get codeWsDeliveryUnavailable =>
      '当前会话没有受管隔离工作区，请新建支持 Git Worktree 的项目会话。';

  @override
  String get codeWsDeliveryStart => '开始统一交付';

  @override
  String get codeWsDeliveryRetry => '重新交付';

  @override
  String get codeWsDeliveryDeliverLatest => '交付后续提交';

  @override
  String get codeWsDeliveryConfirmTitle => '开始统一交付？';

  @override
  String get codeWsDeliveryConfirmDescription =>
      '系统会固化当前已保存提交，完成同步、合并、质量检查和推送。交付期间产生的后续提交会保留，可再次交付。';

  @override
  String get codeWsDeliveryCancel => '暂不交付';

  @override
  String get codeWsDeliveryConfirm => '确认交付';

  @override
  String codeWsDeliveryReviewSummary(int files, int additions, int deletions) {
    return '本次交付 $files 个文件（+$additions −$deletions）';
  }

  @override
  String get codeWsDeliveryReviewNotReady => '变更还没有可交付的提交，或仍有未保存的修改，请先保存后再交付。';

  @override
  String get codeWsDeliveryStarted => '统一交付已进入队列';

  @override
  String get codeWsDeliveryFailed => '交付失败';

  @override
  String get codeWsDeliveryConflict => '检测到合并冲突';

  @override
  String get codeWsDeliveryConflictHint => '请先在电脑端打开该会话的隔离工作区，解决以下冲突并提交，再返回重试。';

  @override
  String get codeWsDeliveryCompleted => '已完成合并与推送';

  @override
  String get codeWsDeliveryPending => '待继续交付';

  @override
  String get codeWsDeliveryCompletedWithPending => '本批已交付，仍有后续提交待交付';

  @override
  String get codeWsDeliveryCompletedWithUnsaved => '本批已交付，后续修改尚未保存，请先在电脑端保存提交';

  @override
  String get codeWsDeliveryQueued => '等待交付';

  @override
  String get codeWsDeliveryRunning => '正在交付';

  @override
  String codeWsDeliveryQueuePosition(int position) {
    return '队列第 $position 位';
  }

  @override
  String codeWsDeliveryAttempt(int attempt) {
    return '第 $attempt 次尝试';
  }

  @override
  String get codeWsDeliveryStageStoppingTerminal => '正在停止宿主终端';

  @override
  String get codeWsDeliveryStageSyncing => '正在同步远程分支';

  @override
  String get codeWsDeliveryStageMerging => '正在合并会话变更';

  @override
  String get codeWsDeliveryStageQualityCheck => '正在执行质量检查';

  @override
  String get codeWsDeliveryStagePushing => '正在推送主分支';

  @override
  String get codeWsDeliveryStageVerifying => '正在核验远端提交';

  @override
  String get codeWsDeliveryStageCleaning => '正在清理会话工作区';

  @override
  String get codeWsQualityTitle => '质量门禁';

  @override
  String get codeWsQualityRun => '运行';

  @override
  String get codeWsQualityRunAll => '全部运行';

  @override
  String get codeWsQualityRunning => '正在执行检查...';

  @override
  String get codeWsQualityEmpty => '当前项目未检测到可运行的质量检查项';

  @override
  String get codeWsQualityEmptyHint =>
      '可在项目 package.json scripts 或 Makefile 中添加 test/lint 脚本。';

  @override
  String get codeWsQualityPassed => '已通过';

  @override
  String get codeWsQualityFailed => '未通过';

  @override
  String get codeWsQualityPending => '未运行';

  @override
  String codeWsQualityExitCode(int code) {
    return '退出码 $code';
  }

  @override
  String codeWsQualityDuration(int duration) {
    return '耗时 $duration ms';
  }

  @override
  String get codeWsQualityLoadFailed => '质量检查加载失败';

  @override
  String get codeWsQualityRunFailed => '质量检查执行失败';

  @override
  String get codeWsMemoryExtract => '沉淀为项目记忆';

  @override
  String get codeWsMemoryExtracting => '正在提取会话记忆...';

  @override
  String get codeWsMemoryExtractSuccess => '已触发记忆提取，分析完成后将自动沉淀为长期规则';

  @override
  String get codeWsMemoryExtractFailed => '记忆提取触发失败';

  @override
  String get codeWsRecoveryAbortInitialization => '放弃初始化？';

  @override
  String get codeWsRecoveryAbortInitializationHint => '将停止当前初始化，已生成的会话记录会保留。';

  @override
  String get gitReviewTitle => '变更审查';

  @override
  String gitReviewSummary(int files) {
    return '$files 个文件';
  }

  @override
  String get gitReviewNoRepository => '无可审查的仓库';

  @override
  String get gitReviewNoChanges => '无未保存修改';

  @override
  String get gitReviewLoadFailed => '加载失败';

  @override
  String get gitReviewDetached => '未绑定分支';

  @override
  String get gitReviewStaged => '已暂存';

  @override
  String get gitReviewChanged => '未暂存';

  @override
  String get gitReviewUntracked => '新文件';

  @override
  String gitReviewSavedCommits(int count) {
    return '已保存 $count 个提交';
  }

  @override
  String get gitReviewTruncated => '仅显示前 500 项';

  @override
  String get gitReviewWorkingDiff => '未暂存差异';

  @override
  String get gitReviewStagedDiff => '已暂存差异';

  @override
  String get gitReviewDiffTitle => '文件差异';

  @override
  String get gitReviewDiffLoadFailed => '差异加载失败';

  @override
  String get gitReviewDiffEmpty => '无文本差异';

  @override
  String get gitReviewDiffTruncated => '仅显示前 1 MB';

  @override
  String get gitReviewCopyDiff => '复制差异';

  @override
  String get gitReviewDiffCopied => '已复制';

  @override
  String get gitReviewSave => '保存修改';

  @override
  String get gitReviewSaveUnavailable => '仅隔离工作区可保存';

  @override
  String get gitReviewSaveTitle => '保存全部修改？';

  @override
  String get gitReviewSaveHint => '检查文件安全后提交到 Git';

  @override
  String get gitReviewMessage => '提交说明';

  @override
  String get gitReviewMessageHint => '留空用默认说明';

  @override
  String get gitReviewCancel => '取消';

  @override
  String get gitReviewConfirmSave => '确认保存';

  @override
  String get gitReviewSaveSuccess => '已提交';

  @override
  String get gitReviewSaveFailed => '保存失败';

  @override
  String get codeFlowTitle => '交付预览';

  @override
  String get codeFlowStartHint => '用本次提交构建并部署到预览';

  @override
  String get codeFlowAutoStartHint => '交付后自动生成预览';

  @override
  String get codeFlowAutoStartPending => '正在生成预览…';

  @override
  String get codeFlowStart => '生成预览';

  @override
  String get codeFlowStarted => '已开始';

  @override
  String get codeFlowOpenPreview => '打开预览';

  @override
  String codeFlowVersion(String version) {
    return 'v$version';
  }

  @override
  String get codeFlowStatusQueued => '排队中';

  @override
  String get codeFlowStatusRunning => '执行中';

  @override
  String get codeFlowStatusSuccess => '已发布';

  @override
  String get codeFlowStatusFailed => '失败';

  @override
  String get codeFlowStatusWaitingDeployment => '待发布';

  @override
  String get codeFlowStageBuilding => '构建中';

  @override
  String get codeFlowStagePublishing => '生成 Release';

  @override
  String get codeFlowStageDeploying => '部署中';

  @override
  String get codeFlowEnvironmentPreview => '预览环境';

  @override
  String get codeFlowEnvironmentProduction => '生产环境';

  @override
  String get codeFlowStatePending => '待部署';

  @override
  String get codeFlowStateDeploying => '部署中';

  @override
  String get codeFlowStateFailed => '部署失败';

  @override
  String get codeFlowStateCurrent => '当前在线';

  @override
  String get codeFlowStateReplaced => '已被替换';

  @override
  String get codeFlowActionDeploy => '部署';

  @override
  String get codeFlowActionRelease => '发布生产';

  @override
  String get codeFlowActionRedeploy => '重新部署';

  @override
  String get codeFlowActionSwitchBack => '切回此版本';

  @override
  String codeFlowConfirmTitle(String environment) {
    return '部署到$environment？';
  }

  @override
  String codeFlowConfirmPreview(
    String version,
    String environment,
    String website,
  ) {
    return '把 v$version 部署到$environment（$website）？失败会保留旧版本。';
  }

  @override
  String codeFlowConfirmProduction(String version, String website) {
    return '把 v$version 发布到生产（$website），会切换线上流量；故障会自动回滚。';
  }

  @override
  String get codeFlowConfirm => '确认部署';

  @override
  String get codeFlowCancel => '取消';

  @override
  String get codeFlowDeployQueued => '已进入部署队列';

  @override
  String get codeFlowBlockedRunnerTargetStale => '该版本运行环境已被替换，需在电脑端重新构建。';

  @override
  String get codeFlowBlockedRunnerTargetUnavailable =>
      'Runner 容器已清理，需在电脑端重新构建。';

  @override
  String get codeFlowBlockedRunnerFixedPort =>
      '流水线用固定端口，预览与生产无法并存；请在电脑端改为自动分配端口。';

  @override
  String get codeFlowOpenFailed => '打开预览失败';

  @override
  String get authEntranceInvalid => '只能包含字母、数字、下划线和短横线';

  @override
  String get authOperationTimeout => '操作超时，请检查网络或系统钥匙串权限';

  @override
  String authConnectFailed(String detail) {
    return '连接或登录失败：$detail';
  }

  @override
  String authQrVerifyFailed(String detail) {
    return '扫码授权验证失败：$detail';
  }

  @override
  String authSwitchFailed(String detail) {
    return '切换服务器失败：$detail';
  }

  @override
  String get codeWsModelPickerSearchHint => '搜索模型名称、厂商或 ID…';

  @override
  String get codeWsModelPickerCustomInput => '手动输入模型 ID';

  @override
  String get codeWsModelPickerCustomHint => '例如 anthropic/claude-sonnet-4-6';

  @override
  String get codeWsModelPickerEmpty => '未找到匹配的模型';

  @override
  String get codeWsModelPickerDefaultSubtitle => '跟随当前执行器默认配置';

  @override
  String get codeWsProjectPickerSearchHint => '搜索项目名称或路径…';

  @override
  String get codeWsProjectPickerEmpty => '未找到匹配的项目';

  @override
  String get chatCameraPermissionDenied => '没有相机或相册权限，没有发送图片';

  @override
  String get chatActionPickGallery => '从相册选择图片';
}
