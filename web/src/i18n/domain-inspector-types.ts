// web/src/i18n/domain-inspector-types.ts

export interface InspectorTabsI18n {
  diffs: string;
  memory: string;
  files: string;
  terminal: string;
  maximize: string;
  restore: string;
}

export interface InspectorDiffsI18n {
  empty: string;
  emptyDesc: string;
  busyEmptyTitle: string;
  busyEmptyDesc: string;
  cleanStatus: string;
  envCardTitle: string;
  envStatusLabel: string;
  envStatusClean: string;
  envBranchLabel: string;
  envModeIsolated: string;
  envModeMain: string;
  envCommitLabel: string;
  envDirLabel: string;
  planModeTipTitle: string;
  planModeTipDesc: string;
  switchToBuildBtn: string;
  checkChangesBtn: string;
  checkingChangesBtn: string;
  browseFilesBtn: string;
  openTerminalBtn: string;
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
  treeTitle: string;
  clickFileHint: string;
  backToTree: string;
  prevFileTooltip: string;
  nextFileTooltip: string;
  expandAll: string;
  collapseAll: string;
  statusAdded: string;
  statusModified: string;
  statusDeleted: string;
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
  sessionMemoryTab: string;
  projectMemoryTab: string;
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
