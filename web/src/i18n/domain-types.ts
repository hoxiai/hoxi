// web/src/i18n/domain-types.ts

export interface CommonI18n {
  ready: string;
  connecting: string;
  working: string;
  cancel: string;
  confirm: string;
  save: string;
  delete: string;
  close: string;
  apply: string;
  copy: string;
  copied: string;
  refresh: string;
  today: string;
  yesterday: string;
  search: string;
  loading: string;
  error: string;
  success: string;
  new: string;
}

export interface HeaderI18n {
  applyToMain: string;
  applying: string;
  appliedSuccess: string;
  applyFailed: string;
  applyTooltip: string;
  undoRevert: string;
  undoRevertTooltip: string;
  lspTooltip: string;
  modelsBtn: string;
  modelsTooltip: string;
  themeLightTooltip: string;
  themeDarkTooltip: string;
  langTooltip: string;
  inspectorShow: string;
  inspectorHide: string;
  switchFolder: string;
}

export interface LeftNavI18n {
  newThread: string;
  creating: string;
  multiProject: string;
  singleProject: string;
  addProjectFolder: string;
  noThreads: string;
  noThreadsHint: string;
  discardSession: string;
  discardConfirm: string;
  untitled: string;
  removeProject: string;
  removeProjectConfirm: string;
  activeWorkspaces: string;
  switchProject: string;
  switchSidebarMode: string;
  filterPlaceholder: string;
  deleteSessionTooltip: string;
  renameSessionTooltip: string;
  renamePlaceholder: string;
  accountAndProviders: string;
  settingsAndProviders: string;
}

export interface TimelineI18n {
  welcomeTitle: string;
  welcomeSubtitle: string;
  welcomeQuestion: string;
  welcomePrompt1: string;
  welcomePrompt2: string;
  welcomePrompt3: string;
  thoughtFor: string;
  thinking: string;
  thought: string;
  showThought: string;
  hideThought: string;
  revertHere: string;
  forkHere: string;
  revertConfirm: string;
  forkConfirm: string;
  steer: string;
  stop: string;
  send: string;
  buildMode: string;
  planMode: string;
  buildDesc: string;
  planDesc: string;
  inputPlaceholder: string;
  steerPlaceholder: string;
  filesAttached: string;
  addContext: string;
  selectModel: string;
  reads: string;
  searches: string;
  lists: string;
  thoughts: string;
  contextTooltip: string;
  contextWatermark: string;
  compactAction: string;
  inputTokens: string;
  outputTokens: string;
  reasoningTokens: string;
  cacheReadTokens: string;
  sessionCost: string;
  planPromptText: string;
  implementPlanSessionTitle: string;
  worktreePromptTemplate: string;
  planReadyTitle: string;
  implementNow: string;
  worktreeTooltip: string;
  newWorktree: string;
  dismiss: string;
  attachImageTooltip: string;
  arenaTooltip: string;
  arenaBtn: string;
  openInInspectorTooltip: string;
  removeCommentTooltip: string;
  copyPromptTooltip: string;
  forkTooltip: string;
  forkBtn: string;
  revertTooltip: string;
  revertBtn: string;
  openTerminalTooltip: string;
  terminalBtn: string;
  reviewCommentSingular: string;
  reviewCommentPlural: string;
  filesCount: string;
  clearAll: string;
  tasksProgress: string;
  filesChangedCount: string;
  reviewDiffs: string;
  modeTooltip: string;
  agentModes: string;
  agentsAvailable: string;
  timelineLabel: string;
  turnsCount: string;
  turnsMenuTooltip: string;
  turnsMenuTitle: string;
  searchTrigger: string;
  searchTooltip: string;
  searchPlaceholder: string;
  matchCaseTooltip: string;
  wholeWordTooltip: string;
  regexTooltip: string;
  invalidRegex: string;
  noResults: string;
  prevMatchTooltip: string;
  nextMatchTooltip: string;
  subagentStack: string;
  subagentsRunning: string;
  subagentsDone: string;
  subagentsWaiting: string;
  stopAllSubagents: string;
  stopSubagentTooltip: string;
  openSubagent: string;
  revertBannerCount: string;
  redoOneTurn: string;
  redoAll: string;
  revertBannerHint: string;
  mentionChangesDetail: string;
  mentionSessionDetail: string;
  mentionWorktreeDetail: string;
  mentionWorkspaceFile: string;
  pastedLinesChip: string;
  slashCommandsHeader: string;
  mentionFileOrContextHeader: string;
  modelRequestFailed: string;
  apiError: string;
  retryWithModel: string;
  switchAndRetryFree: string;
  configureModels: string;
  retryingInSeconds: string;
  retryAttempt: string;
  filesUpdatedCount: string;
  copyResponse: string;
  subagentLiveActivity: string;
  openChildSession: string;
  imagePromptSessionTitle: string;
  relayDegradedWarningTitle: string;
  relayDegradedWarningDesc: string;
  switchRecommendedModel: string;
  turnCompleted: string;
  stepsCompleted: string;
  worktreeClean: string;
  worktreeIsolatedBadge: string;
  auditRunningBadge: string;
  auditPassedBadge: string;
  auditFailedBadge: string;
  auditFixWithFeedbackBtn: string;
  auditSwitchAndFixBtn: string;
  auditFeedbackPromptPrefix: string;
}

export interface ToolCardI18n {
  statePending: string;
  stateRunning: string;
  stateCompleted: string;
  stateError: string;
  input: string;
  output: string;
  diff: string;
  file: string;
  elapsed: string;
  inspectSubagent: string;
  undo: string;
  loaded: string;
  arguments: string;
  error: string;
  inlineChangePreview: string;
  openInDiffInspector: string;
  revertTooltip: string;
}

export interface ApprovalI18n {
  title: string;
  warning: string;
  allowOnce: string;
  allowAlways: string;
  reject: string;
  rejectWithFeedback: string;
  rejectFeedbackPlaceholder: string;
  command: string;
  path: string;
  questionTitle: string;
  skipQuestionTooltip: string;
  multiSelect: string;
  customAnswerLabel: string;
  customAnswerPlaceholder: string;
  confirmHint: string;
  skipQuestion: string;
  submitAnswer: string;
  openDiffInInspector: string;
  autoApproveRules: string;
  ruleAlwaysAllow: string;
  ruleAlwaysDeny: string;
}

export interface InspectorTabsI18n {
  diffs: string;
  memory: string;
  files: string;
  terminal: string;
}

export interface InspectorDiffsI18n {
  empty: string;
  emptyDesc: string;
  filesChanged: string;
  files: string;
  unified: string;
  split: string;
  refresh: string;
  comment: string;
  openFile: string;
  reviewNoteOn: string;
  lineNumPlaceholder: string;
  commentPlaceholder: string;
  addToReview: string;
  noPatch: string;
  revertFileTooltip: string;
  revertFileBtn: string;
  commitPlaceholder: string;
  generateCommitTooltip: string;
  generateCommitBtn: string;
  commitBtn: string;
  committing: string;
  commitSuccess: string;
  auditBtn: string;
  auditingBtn: string;
  auditTooltip: string;
  auditCardTitlePass: string;
  auditCardTitleFail: string;
  auditObserverBy: string;
}

export interface InspectorMemoryI18n {
  title: string;
  empty: string;
  emptyDesc: string;
  goals: string;
  constraints: string;
  keyFacts: string;
  changedFiles: string;
  errorsAndFixes: string;
  todo: string;
  progress: string;
  nextSteps: string;
  save: string;
  notFound: string;
  reload: string;
}

export interface InspectorFilesI18n {
  searchPlaceholder: string;
  noFiles: string;
  noMatchingFiles: string;
  noFilesInWorkspace: string;
  back: string;
}

export interface InspectorTerminalI18n {
  connected: string;
  disconnected: string;
  reconnect: string;
  clear: string;
  connecting: string;
  error: string;
}

export interface InspectorI18n {
  tabs: InspectorTabsI18n;
  diffs: InspectorDiffsI18n;
  memory: InspectorMemoryI18n;
  files: InspectorFilesI18n;
  terminal: InspectorTerminalI18n;
}

export * from "./domain-models-types";

