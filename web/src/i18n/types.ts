// web/src/i18n/types.ts

export type Locale = "zh-CN" | "en-US";

export interface LocaleMetadata {
  id: Locale;
  label: string;
  shortLabel: string;
  langTag: string;
}

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

export interface ModelsI18n {
  modalTitle: string;
  modalSubtitle: string;
  tabCatalog: string;
  tabCustom: string;
  tabPolicy: string;
  searchProviders: string;
  apiKey: string;
  apiKeyPlaceholder: string;
  apiKeyConnectedPlaceholder: string;
  baseUrl: string;
  baseUrlPlaceholder: string;
  customBaseUrl: string;
  customBaseUrlHint: string;
  optional: string;
  registerExtraModel: string;
  extraModelPlaceholder: string;
  keysStoreNote: string;
  saveAndConnect: string;
  connect: string;
  connected: string;
  disconnect: string;
  testConnection: string;
  testing: string;
  active: string;
  selectModel: string;
  addCustomEndpoint: string;
  customEndpointIntro: string;
  providerId: string;
  displayName: string;
  modelId: string;
  modelName: string;
  customModelHint: string;
  apiKeyOptional: string;
  saveAndSelectModel: string;
  policyTitle: string;
  policySubtitle: string;
  savePolicy: string;
  fileEdits: string;
  shellCommands: string;
  webFetch: string;
  autoAllow: string;
  ask: string;
  mcpTitle: string;
  mcpSubtitle: string;
  mcpServerId: string;
  localCommand: string;
  remoteUrl: string;
  add: string;
  noMcpServers: string;
  favorites: string;
  searchModels: string;
  manageKeys: string;
  noModelsFound: string;
  connectProviderOrAdd: string;
  needsApiKey: string;
  reasoningEffort: string;
  supportsReasoning: string;
  arenaTitle: string;
  arenaSubtitle: string;
  stepPrompt: string;
  promptPlaceholder: string;
  stepSelectModels: string;
  filterModelsPlaceholder: string;
  worktreePlanNotice: string;
  launchArena: string;
  fileEditsHint: string;
  shellCommandsHint: string;
  webFetchHint: string;
  removeMcpTooltip: string;
  fetchedModelsCount: string;
  selectAll: string;
  deselectAll: string;
  addSelectedModels: string;
  modelsListTitle: string;
  toggleAllReasoning: string;
  toggleAllImage: string;
  addModelRow: string;
  reasoningCap: string;
  imageCap: string;
  toolsCap: string;
  headersTitle: string;
  addHeader: string;
  headerName: string;
  headerValue: string;
  modelIdPlaceholder: string;
  editProvider: string;
  contextWindow: string;
  maxOutput: string;
  freeModel: string;
  providerApi: string;
  fetchModels: string;
  fetchingModels: string;
  fillBaseUrlPrompt: string;
  fetchModelsFailed: string;
  providerIdRequired: string;
  validBaseUrlRequired: string;
  atLeastOneModelRequired: string;
  providerSaved: string;
  saveCustomProviderFailed: string;
  configuredCustomProviders: string;
  newCustomProvider: string;
  providerIdPlaceholder: string;
  displayNamePlaceholder: string;
  customApiKeyPlaceholder: string;
  policyUpdated: string;
  policyUpdateFailed: string;
  mcpAdded: string;
  mcpAddFailed: string;
  mcpRemoved: string;
  mcpRemoveFailed: string;
  enterApiKeyOrCustomConfig: string;
  connectFailed: string;
  oauthFailed: string;
  oauthVerificationFailed: string;
  oauthLogin: string;
  pasteAuthCodePlaceholder: string;
  editCustomEndpoint: string;
  useGlobalDefault: string;
  rolesTitle: string;
  rolesSubtitle: string;
  defaultModelTitle: string;
  defaultModelDesc: string;
  smallModelTitle: string;
  smallModelDesc: string;
  subagentModelTitle: string;
  subagentModelDesc: string;
  compactionModelTitle: string;
  compactionModelDesc: string;
  modeModelsTitle: string;
  modeModelsDesc: string;
  sourceCustom: string;
  sourceEnv: string;
  sourceConfig: string;
  sourceApi: string;
  customProviderCardTitle: string;
  customProviderCardDesc: string;
  configureCustom: string;
  connectedSection: string;
  noConnectedProviders: string;
  modelsCount: string;
  changeKey: string;
  disableProvider: string;
  popularSection: string;
  viewAllProviders: string;
  disabledSection: string;
  enableProvider: string;
  tabModels: string;
  allConnected: string;
  recent: string;
}

export interface I18nDictionary {
  common: CommonI18n;
  header: HeaderI18n;
  leftNav: LeftNavI18n;
  timeline: TimelineI18n;
  toolCard: ToolCardI18n;
  approval: ApprovalI18n;
  inspector: InspectorI18n;
  models: ModelsI18n;
}

type Join<K, P> = K extends string | number
  ? P extends string | number
    ? `${K}${"" extends P ? "" : "."}${P}`
    : never
  : never;

type Leaves<T, D extends number = 5> = [D] extends [0]
  ? never
  : T extends object
  ? { [K in keyof T]-?: Join<K, Leaves<T[K], [-1, 0, 1, 2, 3, 4][D]>> }[keyof T]
  : "";

export type TxKeyPath = Leaves<I18nDictionary>;

/**
 * Autocomplete-friendly translation key type.
 * Suggests exact paths like `common.ready` or `header.applyToMain`
 * while still permitting dynamic strings if needed.
 */
export type TxKey = TxKeyPath | (string & {});

export type InterpolationParams = Record<string, string | number>;
