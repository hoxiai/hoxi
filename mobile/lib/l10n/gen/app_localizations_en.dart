// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for English (`en`).
class AppLocalizationsEn extends AppLocalizations {
  AppLocalizationsEn([String locale = 'en']) : super(locale);

  @override
  String get appName => 'Hoxi';

  @override
  String get commonCancel => 'Cancel';

  @override
  String get commonCopy => 'Copy';

  @override
  String get commonRetry => 'Retry';

  @override
  String get commonRefresh => 'Refresh';

  @override
  String get chatSend => 'Send message';

  @override
  String get chatHideKeyboard => 'Hide keyboard';

  @override
  String get loginSubtitle => 'Link Hoxi Desktop or a GoPanel panel';

  @override
  String get loginNodeAddress => 'Device address';

  @override
  String get loginAddressHint =>
      'http://192.168.1.x:port or https://demo.gopanel.run';

  @override
  String get loginAddressRequired => 'Enter the device address';

  @override
  String get loginAddressScheme => 'Enter a full URL starting with http/https';

  @override
  String get loginPassword => 'Password';

  @override
  String get loginPasswordHint =>
      'Enter the password (optional on Hoxi Desktop)';

  @override
  String get loginPasswordRequired => 'Enter the password';

  @override
  String get loginConnect => 'Connect';

  @override
  String get loginScanQr => 'Scan QR code';

  @override
  String get loginConnectFailed => 'Connection failed';

  @override
  String get engineeringAddNode => 'Link a device';

  @override
  String get loginEntranceLabel => 'Security entrance (optional)';

  @override
  String get loginEntranceHint => 'For example: your-entrance';

  @override
  String get loginEntranceInvalid =>
      'Use only letters, numbers, underscores and hyphens';

  @override
  String get loginAdminEmail => 'Administrator email';

  @override
  String get loginAdminEmailHint => 'Enter the administrator email';

  @override
  String get chatModelPickerTitle => 'Select model';

  @override
  String get chatTooltipHoldToTalk => 'Hold to talk';

  @override
  String get chatTooltipCamera => 'Camera';

  @override
  String get chatCameraUnavailable =>
      'Camera unavailable. Check permission; cancelling does not send a photo.';

  @override
  String get chatListeningShort => 'Listening…';

  @override
  String get addNodeHoxiTitle => 'Hoxi Desktop';

  @override
  String get addNodeHoxiSubtitle =>
      'Connect desktop Hoxi workspace for AI coding and development';

  @override
  String get addNodeGopanelTitle => 'GoPanel panel';

  @override
  String get addNodeGopanelSubtitle =>
      'Connect a server panel for hosts, containers, sites and certificates';

  @override
  String get addNodeComingSoon => 'Coming soon';

  @override
  String get loginHoxiTitle => 'Link Hoxi Desktop';

  @override
  String get loginHoxiSubtitle =>
      'Scan desktop QR code, or enter the LAN/relay address';

  @override
  String get loginGopanelTitle => 'Link a GoPanel panel';

  @override
  String get loginGopanelSubtitle =>
      'Sign in with a panel administrator account';

  @override
  String get loginPanelAddress => 'Panel address';

  @override
  String get loginPanelAddressHint => 'https://demo.gopanel.run';

  @override
  String get loginHoxiAddress => 'Desktop address';

  @override
  String get loginHoxiAddressHint => 'http://192.168.x.x:5417 or relay address';

  @override
  String get loginHoxiToken => 'Pairing token (optional)';

  @override
  String get loginHoxiTokenHint =>
      'Enter pairing token if configured on desktop';

  @override
  String get loginHoxiQrHint =>
      'Scan QR code in Hoxi Desktop Settings -> Remote Access';

  @override
  String get loginScanHoxiQr => 'Scan desktop QR code';

  @override
  String get loginScanPanelQr => 'Scan panel QR code';

  @override
  String get loginUsername => 'Account (optional)';

  @override
  String get loginPasswordHintPanel => 'Enter the administrator password';

  @override
  String get settingsDeviceRenameHint =>
      'Give this device a name you will recognize';

  @override
  String get commonSave => 'Save';

  @override
  String get qrErrorMissingUrl => 'Invalid QR code: missing device address';

  @override
  String get qrErrorMissingToken =>
      'Invalid QR code: missing panel address or credentials';

  @override
  String get qrErrorWrongKindPanel =>
      'This is a panel QR code. Go back and choose GoPanel panel';

  @override
  String get qrErrorWrongKindHoxi =>
      'This is a desktop QR code. Go back and choose Hoxi Desktop';

  @override
  String get qrErrorVerifyFailed => 'Connection verification failed';

  @override
  String get qrErrorUnrecognized => 'Unrecognized QR code content';

  @override
  String get qrErrorRelayMissing =>
      'Invalid QR code: missing panel address or node';

  @override
  String get qrErrorRelaySignInFirst =>
      'Sign in to this GoPanel panel first before scanning';

  @override
  String get qrErrorRelayConnectFailed =>
      'Could not connect to this desktop via GoPanel';

  @override
  String get qrAlignHint => 'Align the QR code within the frame';

  @override
  String get qrVerifying => 'Verifying and connecting…';

  @override
  String get serverListUnreachable =>
      'Could not reach this device. Check that it is online and reachable.';

  @override
  String serverListRelayConnectFailed(String name) {
    return 'Could not connect to $name via GoPanel. Check that the desktop is online and the GoPanel sign-in is valid.';
  }

  @override
  String get relayNodesTitle => 'Hoxi Desktop computers via this GoPanel';

  @override
  String get relayNodesSubtitle =>
      'Enter the GoPanel pairing code in Hoxi Desktop Settings → Remote Control via GoPanel to see the desktop here';

  @override
  String relayNodesLoadFailed(String error) {
    return 'Failed to load: $error';
  }

  @override
  String get relayNodesEmpty => 'No paired Hoxi Desktop computers yet';

  @override
  String get relayNodeOnline => 'Online';

  @override
  String get relayNodeOffline => 'Offline';

  @override
  String get commonClose => 'Close';

  @override
  String get containerFilterAll => 'All';

  @override
  String get chatActionUploadDocument =>
      'Upload Device File (PDF / Excel / Doc)';

  @override
  String get chatActionUploadDocumentHint =>
      'Pick a spreadsheet, PDF, Word, or data file from this device';

  @override
  String get codeComposerAttachWorkspaceFile =>
      'Reference Workspace File (@file)';

  @override
  String get codeComposerAttachWorkspaceFileHint =>
      'Select an existing file on the computer as context';

  @override
  String get codeComposerAttachSkill => 'Mount Workspace Skill';

  @override
  String get codeComposerAttachSkillHint =>
      'Apply a specific skill instruction in this turn';

  @override
  String chatFileTooLarge(int limitMb) {
    return 'File exceeds maximum upload size ($limitMb MB)';
  }

  @override
  String get chatFilePickerUnsupported =>
      'Document picking is not supported on this device';

  @override
  String chatFileReadFailed(String error) {
    return 'Could not read file: $error';
  }

  @override
  String get codeWsFilesExportShare => 'Export / Share File';

  @override
  String get codeWsFilesExportUnsupported =>
      'File sharing is not supported on this device';

  @override
  String codeWsFilesExportFailed(String error) {
    return 'Could not export file: $error';
  }

  @override
  String get codeWsFilesBinaryPreviewHint =>
      'Binary file preview is limited on mobile. Tap Export / Share above to save or open in another app.';

  @override
  String get nodeLinkDevicesSection => 'Controlled Devices';

  @override
  String get codeWsFilesTitle => 'Project files';

  @override
  String get codeWsFilesRoot => 'Project root';

  @override
  String get codeWsFilesEmpty => 'No viewable files in this directory';

  @override
  String get codeWsFilesTruncated => 'Only the first 500 items are shown';

  @override
  String get codeWsFilesEditing => 'Editing';

  @override
  String get codeWsFilesSaved => 'Saved';

  @override
  String get codeWsFilesUnsaved => 'Unsaved';

  @override
  String get codeWsFilesSave => 'Save file';

  @override
  String get codeWsFilesSaveSuccess => 'File saved';

  @override
  String get codeWsFilesUnsavedTitle => 'Discard unsaved changes?';

  @override
  String get codeWsFilesUnsavedHint =>
      'Your mobile edits will be lost when you go back.';

  @override
  String get codeWsFilesDiscard => 'Discard';

  @override
  String get codeWsFilesKeepEditing => 'Keep editing';

  @override
  String get codeWsFilesCopy => 'Copy content';

  @override
  String get codeWsFilesCopied => 'File content copied';

  @override
  String get codeWsFilesOperationFailed => 'File operation failed';

  @override
  String get codeWsActionBack => 'Back';

  @override
  String get codeWsActionParent => 'Parent folder';

  @override
  String get codeWsActionRefresh => 'Refresh';

  @override
  String get codeWsActionRetry => 'Retry';

  @override
  String get codeWsActionStop => 'Stop execution';

  @override
  String get codeWsActionDismiss => 'Dismiss';

  @override
  String get codeWsChatTitle => 'Hoxi Development';

  @override
  String get codeWsChatStreamReconnecting =>
      'Live connection lost. Reconnecting…';

  @override
  String get codeWsChatRefresh => 'Refresh status';

  @override
  String get codeWsChatGitReview => 'Review changes';

  @override
  String get codeWsChatChooseSession => 'Choose or create session';

  @override
  String get codeWsChatPreviewTitle => 'Session previews';

  @override
  String get codeWsChatEmptyTitle => 'Start a development session';

  @override
  String get codeWsChatEmptyHint =>
      'Connect a project and server executor to send instructions, follow progress, open previews, and handle approvals.';

  @override
  String codeWsChatSessionFallback(int id) {
    return 'Development #$id';
  }

  @override
  String get codeWsSessionSettingsTitle => 'Session settings';

  @override
  String get codeWsSessionSettingsName => 'Session name';

  @override
  String get codeWsSessionSettingsApproval => 'Approval policy';

  @override
  String get codeWsSessionSettingsPolicyManual => 'Confirm each time';

  @override
  String get codeWsSessionSettingsPolicySafeAuto => 'Safe auto';

  @override
  String get codeWsSessionSettingsPolicyFullAuto => 'Full auto';

  @override
  String codeWsSessionSettingsPolicyRejectsInConversation(String executor) {
    return '$executor cannot ask you in conversation mode: actions that need permission (such as running commands) are rejected outright, so the AI may not finish the task. Approve them one by one in terminal mode instead.';
  }

  @override
  String get codeWsSessionSettingsModel => 'Model';

  @override
  String get codeWsSessionSettingsModelHint =>
      'Leave empty for the executor default';

  @override
  String get codeWsSessionSettingsEffort => 'Reasoning effort';

  @override
  String get codeWsSessionSettingsEffortDefault => 'Default';

  @override
  String get codeWsSessionSettingsSave => 'Save settings';

  @override
  String get codeWsSessionSettingsSaved => 'Session settings saved';

  @override
  String get codeWsAttentionTitle => 'Needs attention';

  @override
  String get codeWsAttentionViewAll => 'View all';

  @override
  String get codeWsAttentionEmpty => 'No Code items need your attention';

  @override
  String get codeWsAttentionLoadFailed => 'Failed to load attention items';

  @override
  String codeWsAttentionSession(int id) {
    return 'Development session #$id';
  }

  @override
  String get codeWsAttentionConfirmHint =>
      'This action immediately affects the development session. Continue?';

  @override
  String get codeWsAttentionCancel => 'Cancel';

  @override
  String get codeWsAttentionConfirm => 'Confirm';

  @override
  String get codeWsAttentionActionSuccess => 'Action completed';

  @override
  String get codeWsAttentionActionFailed => 'Action failed. Try again later.';

  @override
  String get codeWsAttentionOpenFailed =>
      'Failed to open the development session';

  @override
  String get codeWsInstructionClosedHint =>
      'This session is being delivered and cannot accept more changes';

  @override
  String get codeWsInstructionInputHint =>
      'Enter an instruction or follow-up...';

  @override
  String get codeWsInstructionSelectSessionHint =>
      'Select a development session first';

  @override
  String get codeWsInstructionSend => 'Send instruction';

  @override
  String get codeWsHubTitle => 'Development command center';

  @override
  String get codeWsHubRecentTasks => 'Recent';

  @override
  String get codeWsHubProjectsTab => 'Projects';

  @override
  String codeWsHubSessionCount(int count) {
    return '$count';
  }

  @override
  String get codeWsHubProject => 'Current project';

  @override
  String get codeWsHubNoProjectTasks => 'No tasks';

  @override
  String get codeWsHubNoProjectTasksHint =>
      'Create a session to add a task here.';

  @override
  String get codeWsHubCreate => 'New';

  @override
  String get codeWsHubProjectTerminal => 'Project terminal';

  @override
  String get codeWsHubProjectTerminalHint =>
      'Open the project directory for command-line work and quick diagnostics.';

  @override
  String get codeWsTerminalChooseProject => 'Choose a project';

  @override
  String get codeWsTerminalChooseProjectHint =>
      'The terminal starts in the project directory. Review high-risk commands carefully.';

  @override
  String get codeWsTerminalNoProjects =>
      'No development projects yet. Create one in the admin console first.';

  @override
  String get codeWsTerminalOpenFailed => 'Failed to open project terminal';

  @override
  String get codeWsTerminalOpening => 'Opening terminal...';

  @override
  String get codeWsTerminalProjectTitle => 'Project terminal';

  @override
  String get codeWsTerminalNoTask => 'No task';

  @override
  String codeWsTerminalTaskTitle(String task) {
    return 'Task · $task';
  }

  @override
  String get codeWsTerminalReconnect => 'Reconnect';

  @override
  String get codeWsTerminalConnecting => 'Connecting to terminal…';

  @override
  String get codeWsTerminalReconnecting => 'Connection lost. Recovering…';

  @override
  String get codeWsTerminalConnectionFailed =>
      'Terminal connection failed. Check the network and retry.';

  @override
  String get codeWsTerminalSessionClosed => 'Terminal session ended';

  @override
  String get codeWsTerminalRelayUnsupported =>
      'Interactive terminals are unavailable over the GoPanel tunnel. Use a Hoxi desktop node on the local network instead.';

  @override
  String get codeWsTerminalConnected => 'Connected';

  @override
  String get codeWsTerminalReleaseControl => 'Release terminal control';

  @override
  String get codeWsTerminalTakeControl => 'Take terminal control';

  @override
  String get codeWsTerminalSessionStatus => 'Session status and instructions';

  @override
  String get codeWsTerminalFiles => 'Files';

  @override
  String get codeWsTerminalCommandHint =>
      'Type a command; long and composed input supported';

  @override
  String get codeWsTerminalDisconnected => 'Terminal disconnected';

  @override
  String get codeWsTerminalTakeControlHint =>
      'Tap the input to take terminal control';

  @override
  String get codeWsTerminalSendCommand => 'Send command';

  @override
  String get codeWsTerminalHistoryPrevious => '↑ History';

  @override
  String get codeWsTerminalHistoryNext => '↓ History';

  @override
  String get codeWsTerminalPaste => 'Paste';

  @override
  String get codeWsTerminalNativeKeyboard => 'Terminal keyboard';

  @override
  String get codeWsSessionIsolateLabel => 'Isolate workspace';

  @override
  String get codeWsSessionIsolateNo => 'No';

  @override
  String get codeWsSessionIsolateYes => 'Yes';

  @override
  String get codeWsSessionWorktreeChecking => 'Checking workspace isolation...';

  @override
  String get codeWsSessionWorktreeReady => 'Isolated workspace enabled';

  @override
  String get codeWsSessionWorktreeReadyHint =>
      'This session runs in a separate Git Worktree without editing the project directory directly.';

  @override
  String get codeWsSessionWorktreeDirty => 'Uncommitted changes detected';

  @override
  String codeWsSessionWorktreeDirtyHint(String repositories) {
    return 'Resolve these repositories on desktop before creating a session: $repositories';
  }

  @override
  String get codeWsSessionWorktreeUnavailable =>
      'This project cannot create a safe session';

  @override
  String get codeWsSessionWorktreeReasonNotGit =>
      'The project directory is not a Git repository. Configure it on desktop first.';

  @override
  String get codeWsSessionWorktreeReasonSourceUnavailable =>
      'The project source directory is unavailable. Check its desktop configuration.';

  @override
  String get codeWsSessionWorktreeReasonUnknown =>
      'Git Worktree isolation is unavailable for this project.';

  @override
  String get codeWsSessionWorktreeCheckFailed =>
      'Workspace isolation check failed';

  @override
  String get codeWsSessionWorktreeRetry => 'Check again';

  @override
  String get codeWsStopTitle => 'Stop the current Code task?';

  @override
  String get codeWsStopDescription =>
      'Running and queued instructions will stop. Existing file changes are preserved.';

  @override
  String get codeWsStopCancel => 'Keep running';

  @override
  String get codeWsStopConfirm => 'Stop';

  @override
  String get codeWsStopSuccess => 'Stop request sent';

  @override
  String get codeWsRecoveryTitle => 'Session history and recovery';

  @override
  String get codeWsRecoveryInitialization => 'Session initialization';

  @override
  String get codeWsRecoveryInitializationFailed => 'Initialization failed';

  @override
  String get codeWsRecoveryInitializationFailedHint =>
      'The session environment failed to initialize. Retry or review the error details.';

  @override
  String get codeWsRecoveryInitializing => 'Initializing';

  @override
  String get codeWsRecoveryInitialized => 'Initialized';

  @override
  String get codeWsRecoveryInitializingHint =>
      'Preparing the isolated workspace and execution environment.';

  @override
  String get codeWsRecoveryInitializedHint =>
      'The execution environment is ready for more instructions.';

  @override
  String get codeWsRecoveryRetryInitialization => 'Retry initialization';

  @override
  String get codeWsRecoveryRetryInitializationHint =>
      'Hoxi will prepare this session workspace and execution environment again. Continue?';

  @override
  String get codeWsRecoveryConversation => 'Full conversation';

  @override
  String get codeWsRecoveryNoMessages =>
      'No historical messages in this session';

  @override
  String get codeWsRecoveryRuns => 'Execution runs';

  @override
  String get codeWsRecoveryNoRuns => 'No execution runs in this session';

  @override
  String get codeWsRecoveryViewRunDetail => 'View details';

  @override
  String get codeWsRecoveryLoadMore => 'Load more';

  @override
  String get codeWsRecoveryUser => 'You';

  @override
  String get codeWsRecoveryAgent => 'Code assistant';

  @override
  String codeWsRecoveryDuration(int duration) {
    return '$duration ms';
  }

  @override
  String codeWsRecoveryTokens(int tokens) {
    return '$tokens tokens';
  }

  @override
  String get codeWsRecoveryRetryInstruction => 'Retry instruction';

  @override
  String get codeWsRecoveryRetryInstructionHint =>
      'This failed or stopped instruction will run again. Continue?';

  @override
  String get codeWsRecoveryRetryQueued => 'Queued again';

  @override
  String get codeWsRecoveryStatusCompleted => 'Completed';

  @override
  String get codeWsRecoveryStatusFailed => 'Failed';

  @override
  String get codeWsRecoveryStatusStopped => 'Stopped';

  @override
  String get codeWsRecoveryStatusRunning => 'Running';

  @override
  String get codeWsRecoveryStatusAwaitingApproval => 'Awaiting approval';

  @override
  String get codeWsRecoveryStatusQueued => 'Queued';

  @override
  String get codeWsRecoveryStatusUnknown => 'Unknown';

  @override
  String get codeWsRecoveryLoadFailed => 'Failed to load session history';

  @override
  String get codeWsRecoveryCancel => 'Cancel';

  @override
  String get codeWsRecoveryConfirm => 'Confirm';

  @override
  String get codeWsRecoveryActionSuccess => 'Action submitted';

  @override
  String get codeWsRecoveryActionFailed => 'Action failed. Try again later.';

  @override
  String codeWsRunDetailTitle(int id) {
    return 'Run #$id';
  }

  @override
  String get codeWsRunDetailCopy => 'Copy diagnostics';

  @override
  String get codeWsRunDetailCopied => 'Diagnostics copied';

  @override
  String get codeWsRunDetailLoadFailed => 'Failed to load run details';

  @override
  String get codeWsRunDetailEmpty => 'No run details available';

  @override
  String get codeWsRunDetailOverview => 'Run overview';

  @override
  String get codeWsRunDetailStatus => 'Status';

  @override
  String get codeWsRunDetailExecutor => 'Executor';

  @override
  String get codeWsRunDetailModel => 'Model';

  @override
  String get codeWsRunDetailExitCode => 'Exit code';

  @override
  String get codeWsRunDetailDurationLabel => 'Duration';

  @override
  String get codeWsRunDetailSession => 'Session';

  @override
  String get codeWsRunDetailTask => 'Task';

  @override
  String get codeWsRunDetailInstruction => 'Instruction';

  @override
  String get codeWsRunDetailStartedAt => 'Started';

  @override
  String get codeWsRunDetailCompletedAt => 'Completed';

  @override
  String get codeWsRunDetailInputTokens => 'Input tokens';

  @override
  String get codeWsRunDetailOutputTokens => 'Output tokens';

  @override
  String get codeWsRunDetailCachedTokens => 'Cached input tokens';

  @override
  String get codeWsRunDetailReasoningTokens => 'Reasoning tokens';

  @override
  String get codeWsRunDetailTotalTokens => 'Total tokens';

  @override
  String get codeWsRunDetailPrompt => 'Original instruction';

  @override
  String get codeWsRunDetailError => 'Error details';

  @override
  String get codeWsRunDetailOutput => 'Execution output';

  @override
  String get codeWsRunDetailRawOutput => 'Raw logs';

  @override
  String get codeWsRunDetailNoPrompt => 'No original instruction recorded';

  @override
  String get codeWsRunDetailNoOutput => 'No structured execution output';

  @override
  String get codeWsRunDetailNoRawOutput => 'No raw logs available';

  @override
  String get codeWsDeliveryTitle => 'Unified delivery';

  @override
  String get codeWsDeliveryEmpty =>
      'Merge this session into the project branch and push it through the managed delivery flow.';

  @override
  String get codeWsDeliveryUnavailable =>
      'This session has no managed worktree. Create a project session with Git Worktree support.';

  @override
  String get codeWsDeliveryStart => 'Start delivery';

  @override
  String get codeWsDeliveryRetry => 'Retry delivery';

  @override
  String get codeWsDeliveryDeliverLatest => 'Deliver newer commits';

  @override
  String get codeWsDeliveryConfirmTitle => 'Start unified delivery?';

  @override
  String get codeWsDeliveryConfirmDescription =>
      'Hoxi snapshots the saved commits, then syncs, merges, runs quality checks, and pushes. Newer commits remain available for another delivery.';

  @override
  String get codeWsDeliveryCancel => 'Not now';

  @override
  String get codeWsDeliveryConfirm => 'Deliver';

  @override
  String codeWsDeliveryReviewSummary(int files, int additions, int deletions) {
    return 'This delivery covers $files files (+$additions −$deletions)';
  }

  @override
  String get codeWsDeliveryReviewNotReady =>
      'There are no deliverable commits yet, or some changes are still unsaved. Save them before delivering.';

  @override
  String get codeWsDeliveryStarted => 'Delivery queued';

  @override
  String get codeWsDeliveryFailed => 'Delivery failed';

  @override
  String get codeWsDeliveryConflict => 'Merge conflicts detected';

  @override
  String get codeWsDeliveryConflictHint =>
      'Open this session worktree on desktop, resolve and commit these conflicts, then retry here.';

  @override
  String get codeWsDeliveryCompleted => 'Merge and push completed';

  @override
  String get codeWsDeliveryPending => 'More commits pending';

  @override
  String get codeWsDeliveryCompletedWithPending =>
      'This batch completed; newer commits are waiting for delivery';

  @override
  String get codeWsDeliveryCompletedWithUnsaved =>
      'This batch completed; save the newer working tree changes on desktop first';

  @override
  String get codeWsDeliveryQueued => 'Waiting for delivery';

  @override
  String get codeWsDeliveryRunning => 'Delivering';

  @override
  String codeWsDeliveryQueuePosition(int position) {
    return 'Queue position $position';
  }

  @override
  String codeWsDeliveryAttempt(int attempt) {
    return 'Attempt $attempt';
  }

  @override
  String get codeWsDeliveryStageStoppingTerminal => 'Stopping host terminals';

  @override
  String get codeWsDeliveryStageSyncing => 'Syncing the remote branch';

  @override
  String get codeWsDeliveryStageMerging => 'Merging session changes';

  @override
  String get codeWsDeliveryStageQualityCheck => 'Running quality checks';

  @override
  String get codeWsDeliveryStagePushing => 'Pushing the main branch';

  @override
  String get codeWsDeliveryStageVerifying => 'Verifying the remote commit';

  @override
  String get codeWsDeliveryStageCleaning => 'Cleaning the session workspace';

  @override
  String get codeWsQualityTitle => 'Quality Gate';

  @override
  String get codeWsQualityRun => 'Run';

  @override
  String get codeWsQualityRunAll => 'Run All';

  @override
  String get codeWsQualityRunning => 'Running checks...';

  @override
  String get codeWsQualityEmpty => 'No quality check commands detected';

  @override
  String get codeWsQualityEmptyHint =>
      'You can add test or lint scripts to package.json or Makefile.';

  @override
  String get codeWsQualityPassed => 'Passed';

  @override
  String get codeWsQualityFailed => 'Failed';

  @override
  String get codeWsQualityPending => 'Not run';

  @override
  String codeWsQualityExitCode(int code) {
    return 'Exit code $code';
  }

  @override
  String codeWsQualityDuration(int duration) {
    return 'Duration $duration ms';
  }

  @override
  String get codeWsQualityLoadFailed => 'Failed to load quality checks';

  @override
  String get codeWsQualityRunFailed => 'Failed to run quality check';

  @override
  String get codeWsMemoryExtract => 'Extract Project Memory';

  @override
  String get codeWsMemoryExtracting => 'Extracting session memory...';

  @override
  String get codeWsMemoryExtractSuccess =>
      'Memory extraction queued; will persist as long-term rules upon analysis';

  @override
  String get codeWsMemoryExtractFailed => 'Failed to queue memory extraction';

  @override
  String get codeWsRecoveryAbortInitialization => 'Abort initialization?';

  @override
  String get codeWsRecoveryAbortInitializationHint =>
      'This stops the current initialization and keeps the existing session record.';

  @override
  String get gitReviewTitle => 'Review changes';

  @override
  String gitReviewSummary(int files) {
    return '$files files';
  }

  @override
  String get gitReviewNoRepository =>
      'No Git repository is available for this session';

  @override
  String get gitReviewNoChanges => 'The workspace has no unsaved changes';

  @override
  String get gitReviewLoadFailed => 'Failed to load Git changes';

  @override
  String get gitReviewDetached => 'Detached HEAD';

  @override
  String get gitReviewStaged => 'Staged';

  @override
  String get gitReviewChanged => 'Unstaged';

  @override
  String get gitReviewUntracked => 'New file';

  @override
  String gitReviewSavedCommits(int count) {
    return '$count saved commits';
  }

  @override
  String get gitReviewTruncated => 'Only the first 500 changed files are shown';

  @override
  String get gitReviewWorkingDiff => 'View unstaged diff';

  @override
  String get gitReviewStagedDiff => 'View staged diff';

  @override
  String get gitReviewDiffTitle => 'File diff';

  @override
  String get gitReviewDiffLoadFailed => 'Failed to load file diff';

  @override
  String get gitReviewDiffEmpty => 'No text diff is available for this file';

  @override
  String get gitReviewDiffTruncated =>
      'The diff is too large; only the first 1 MB is shown';

  @override
  String get gitReviewCopyDiff => 'Copy diff';

  @override
  String get gitReviewDiffCopied => 'Diff copied';

  @override
  String get gitReviewSave => 'Save changes';

  @override
  String get gitReviewSaveUnavailable =>
      'Only managed isolated workspaces can be saved from mobile';

  @override
  String get gitReviewSaveTitle => 'Save all current changes?';

  @override
  String get gitReviewSaveHint =>
      'Hoxi checks file safety and saves every current workspace change in a Git commit.';

  @override
  String get gitReviewMessage => 'Commit message';

  @override
  String get gitReviewMessageHint => 'Leave blank to use the default message';

  @override
  String get gitReviewCancel => 'Cancel';

  @override
  String get gitReviewConfirmSave => 'Save';

  @override
  String get gitReviewSaveSuccess => 'Changes saved in a Git commit';

  @override
  String get gitReviewSaveFailed =>
      'Save failed. Review the error and try again.';

  @override
  String get codeFlowTitle => 'Delivery preview';

  @override
  String get codeFlowStartHint =>
      'Build this delivery\'s commits and deploy them to the preview environment for acceptance.';

  @override
  String get codeFlowAutoStartHint =>
      'Previews start automatically after delivery.';

  @override
  String get codeFlowAutoStartPending =>
      'Delivery finished. Generating the preview automatically…';

  @override
  String get codeFlowStart => 'Generate preview';

  @override
  String get codeFlowStarted => 'Preview started';

  @override
  String get codeFlowOpenPreview => 'Open preview';

  @override
  String codeFlowVersion(String version) {
    return 'v$version';
  }

  @override
  String get codeFlowStatusQueued => 'Queued';

  @override
  String get codeFlowStatusRunning => 'Running';

  @override
  String get codeFlowStatusSuccess => 'Released';

  @override
  String get codeFlowStatusFailed => 'Failed';

  @override
  String get codeFlowStatusWaitingDeployment => 'Awaiting release';

  @override
  String get codeFlowStageBuilding => 'Building';

  @override
  String get codeFlowStagePublishing => 'Publishing release';

  @override
  String get codeFlowStageDeploying => 'Deploying';

  @override
  String get codeFlowEnvironmentPreview => 'Preview';

  @override
  String get codeFlowEnvironmentProduction => 'Production';

  @override
  String get codeFlowStatePending => 'Pending';

  @override
  String get codeFlowStateDeploying => 'Deploying';

  @override
  String get codeFlowStateFailed => 'Failed';

  @override
  String get codeFlowStateCurrent => 'Live';

  @override
  String get codeFlowStateReplaced => 'Replaced';

  @override
  String get codeFlowActionDeploy => 'Deploy';

  @override
  String get codeFlowActionRelease => 'Release';

  @override
  String get codeFlowActionRedeploy => 'Redeploy';

  @override
  String get codeFlowActionSwitchBack => 'Switch back';

  @override
  String codeFlowConfirmTitle(String environment) {
    return 'Deploy to $environment?';
  }

  @override
  String codeFlowConfirmPreview(
    String version,
    String environment,
    String website,
  ) {
    return 'Deploy v$version to $environment ($website). A health check runs before switching, and the previous version is kept if it fails.';
  }

  @override
  String codeFlowConfirmProduction(String version, String website) {
    return 'Release v$version to production ($website). This switches live traffic. A health check runs before switching, and the previous version is restored automatically if it fails during stabilization.';
  }

  @override
  String get codeFlowConfirm => 'Deploy';

  @override
  String get codeFlowCancel => 'Cancel';

  @override
  String get codeFlowDeployQueued => 'Deployment queued';

  @override
  String get codeFlowBlockedRunnerTargetStale =>
      'A newer build has replaced this version\'s runtime. Rebuild it on desktop before deploying.';

  @override
  String get codeFlowBlockedRunnerTargetUnavailable =>
      'This version\'s Runner container was cleaned up. Rebuild it on desktop before deploying.';

  @override
  String get codeFlowBlockedRunnerFixedPort =>
      'The pipeline uses a fixed host port, so preview and production cannot coexist. Switch to automatic ports on desktop.';

  @override
  String get codeFlowOpenFailed => 'Unable to open the preview URL';

  @override
  String get authEntranceInvalid =>
      'Use only letters, numbers, underscores, and hyphens';

  @override
  String get authOperationTimeout =>
      'The operation timed out. Check the network or keychain access.';

  @override
  String authConnectFailed(String detail) {
    return 'Connection or sign-in failed: $detail';
  }

  @override
  String authQrVerifyFailed(String detail) {
    return 'QR authorization failed: $detail';
  }

  @override
  String authSwitchFailed(String detail) {
    return 'Failed to switch server: $detail';
  }

  @override
  String get codeWsModelPickerSearchHint =>
      'Search model name, provider, or ID…';

  @override
  String get codeWsModelPickerCustomInput => 'Custom model ID';

  @override
  String get codeWsModelPickerCustomHint => 'e.g. anthropic/claude-sonnet-4-6';

  @override
  String get codeWsModelPickerEmpty => 'No matching models found';

  @override
  String get codeWsModelPickerDefaultSubtitle =>
      'Follow current executor default configuration';

  @override
  String get codeWsProjectPickerSearchHint => 'Search project name or path…';

  @override
  String get codeWsProjectPickerEmpty => 'No matching projects found';

  @override
  String get chatCameraPermissionDenied =>
      'Camera or photo library permission denied. No photo was sent.';

  @override
  String get chatActionPickGallery => 'Choose from Photo Library';

  @override
  String get commonCopied => 'Copied';

  @override
  String get chatReportAction => 'Report';

  @override
  String get chatReportedBadge => 'Reported';
}
