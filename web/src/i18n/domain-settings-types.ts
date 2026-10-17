// web/src/i18n/domain-settings-types.ts

export interface SettingsI18n {
  modalTitle: string;
  modalSubtitle: string;
  tabGeneral: string;
  tabChat: string;
  tabWorkspace: string;
  tabTerminal: string;
  tabSystem: string;

  // General & Appearance
  sectionLanguageTitle: string;
  sectionLanguageDesc: string;
  langZhDesc: string;
  langEnDesc: string;
  sectionThemeTitle: string;
  sectionThemeDesc: string;
  themeLight: string;
  themeDark: string;
  themeSystem: string;
  themeLightDesc: string;
  themeDarkDesc: string;
  themeSystemDesc: string;
  sectionFontScaleTitle: string;
  sectionFontScaleDesc: string;
  fontCompact: string;
  fontDefault: string;
  fontComfortable: string;
  fontLarge: string;
  sectionProfileTitle: string;
  sectionProfileDesc: string;
  profileNamePlaceholder: string;

  // Chat & Composer
  sectionSendShortcutTitle: string;
  sectionSendShortcutDesc: string;
  shortcutEnterTitle: string;
  shortcutEnterDesc: string;
  shortcutModEnterTitle: string;
  shortcutModEnterDesc: string;
  sectionReasoningTitle: string;
  sectionReasoningDesc: string;
  reasoningAutoCollapse: string;
  reasoningAutoCollapseDesc: string;
  reasoningExpanded: string;
  reasoningExpandedDesc: string;
  reasoningCollapsed: string;
  reasoningCollapsedDesc: string;
  sectionStreamPrefsTitle: string;
  autoScrollLabel: string;
  autoScrollDesc: string;
  confirmRevertLabel: string;
  confirmRevertDesc: string;
  desktopNotificationsLabel: string;
  desktopNotificationsDesc: string;
  sectionCustomInstructionsTitle: string;
  sectionCustomInstructionsDesc: string;
  customInstructionsPlaceholder: string;

  // Workspace, Git & Diffs
  sectionSandboxTitle: string;
  sectionSandboxDesc: string;
  sandboxLocalTitle: string;
  sandboxLocalDesc: string;
  sandboxWorktreeTitle: string;
  sandboxWorktreeDesc: string;
  sectionSidebarModeTitle: string;
  sectionSidebarModeDesc: string;
  sidebarMultiDesc: string;
  sidebarSingleDesc: string;
  sectionDiffStyleTitle: string;
  sectionDiffStyleDesc: string;
  diffUnifiedTitle: string;
  diffUnifiedDesc: string;
  diffSplitTitle: string;
  diffSplitDesc: string;
  sectionApplyModeTitle: string;
  sectionApplyModeDesc: string;
  applyPatchTitle: string;
  applyPatchDesc: string;
  applyMergeTitle: string;
  applyMergeDesc: string;
  sectionCodeAuditTitle: string;
  sectionCodeAuditDesc: string;
  autoAuditLabel: string;
  autoAuditDesc: string;
  configureObserverBtn: string;

  // Terminal & Safety
  sectionTerminalTitle: string;
  sectionTerminalDesc: string;
  terminalFontSizeLabel: string;
  terminalFontSizeDesc: string;
  terminalCursorBlinkLabel: string;
  terminalCursorBlinkDesc: string;
  sectionSafetyPolicyTitle: string;
  sectionSafetyPolicyDesc: string;
  openFullPolicyModalBtn: string;

  // System & Diagnostics
  sectionRuntimeTitle: string;
  sectionRuntimeDesc: string;
  runtimeStatusLabel: string;
  runtimeBinaryLabel: string;
  runtimeVersionLabel: string;
  runtimePortLabel: string;
  runtimeGatewayLabel: string;
  runtimeConfigPathLabel: string;
  runtimePlatformLabel: string;
  runtimeNotDetected: string;
  sectionMaintenanceTitle: string;
  sectionMaintenanceDesc: string;
  reloadEngineBtn: string;
  reloadingEngineBtn: string;
  reloadEngineSuccess: string;
  manageModelsAndKeysBtn: string;
  resetTelemetryBtn: string;
  resetTelemetrySuccess: string;
  resetSettingsBtn: string;
  resetSettingsConfirm: string;
  resetSettingsSuccess: string;
  shortcutHint: string;
}
