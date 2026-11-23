// web/src/i18n/locales/en-US/companion.ts

export const companion = {
  nameHoxiChan: "Hoxi-chan",
  nameNekoBot: "Neko-01",
  nameCustom: "Companion",
  levelBadge: "Lv.{level}",
  todayTokensLabel: "{tokens} tok today",

  // Mood status badges
  badgeIdle: "Standby",
  badgeCoding: "Coding",
  badgeConvoy: "Parallel x{count}",
  badgeGateDanger: "High-Risk Blocked",
  badgeGateApproval: "Needs Review ({count})",
  badgeFailover: "Auto Rerouting",
  badgeCelebrate: "Turn Complete",

  // State-reactive speech bubble lines
  lineGateDanger: "Hold on! High-risk command [{target}] was intercepted by the safety gate — tap to inspect",
  lineGateApproval: "Waiting on your confirmation ({count} pending) — tap here to review",
  lineFailover: "Upstream hiccup detected (attempt #{attempt}) — holding context and rerouting cleanly",
  lineConvoy: "{count} isolated Worktree lanes are sprinting in parallel! Grab some tea while I referee",
  lineCoding: "Streaming patches and watching tool outputs — steer anytime if the plan drifts",
  lineCelebrate: "Clean landing! Diffs are ready in the right inspector whenever you want to review",

  // Time-of-day & scheme-aware idle greetings
  idleMorning: "Good morning! Local engines are warm — spin up a fresh Worktree or resume yesterday's CLI thread?",
  idleAfternoon: "Afternoon check-in: 30 MB footprint, zero Electron lag, ready for the next refactor",
  idleEvening: "Evening build session! Want to pit two models head-to-head in an isolated Worktree?",
  idleLateNight: "Still up past midnight? Let unattended Seatbelt sandbox finish the grind and get some rest",
  idleObsidian: "Obsidian & Gold engaged — high-contrast dark luxury, zero forced login, pure local speed",

  // Interactive poke lines
  poke1: "Hey, eyes on the diff! Though switching to Sakura glass does boost coding morale by 120%",
  poke2: "Tip: you can hand off live Claude Code, Codex, or Gemini CLI threads with zero context loss",
  poke3: "System proxy 502? Already bypassed at the Go loopback layer — poke me anytime to test",
  poke4: "Completely useless desktop companion. Absolutely essential emotional support for refactoring",

  // Welcome Stage quick actions & labels
  actionHandoff: "Import CLI Session",
  actionSwitchWorktree: "Use Worktree Sandbox",
  actionWorktreeActive: "Worktree Sandbox On",
  actionPoke: "Poke Line",
  actionJumpApproval: "Review Gate →",
  minimizeTooltip: "Minimize companion",
  expandTooltip: "Expand companion",

  // Settings & Header controls
  headerQuickToggle: "ACG Companion",
  sectionTitle: "ACG Companion & Welcome Stage",
  sectionDesc: "Bind an anime operator or cyber-cat mascot to live session states, Worktree convoys, and high-risk command gates",
  enableLabel: "Enable Workspace Companion",
  enableDesc: "Show state-driven mascot reactions for streaming, parallel convoys, failover, and command approvals",
  personaLabel: "Companion Persona",
  personaHoxiChan: "Hoxi-chan (Anime)",
  personaNekoBot: "Neko-01 (Cyber Cat)",
  personaCustom: "Custom Standee",
  welcomeStageLabel: "Empty-Thread Welcome Stage",
  welcomeStageDesc: "Replace the particle wordmark on new threads with an interactive character greeting card",
  cornerDockLabel: "Floating Desktop Pet (Outside Window)",
  cornerDockDesc: "Float an always-on-top draggable pet outside the app window on macOS (falls back to a corner widget in web mode)",
  resetPositionBtn: "Reset Position",
  speechBubbleLabel: "State Speech Bubbles",
  speechBubbleDesc: "Pop dialogue bubbles on high-risk blocks, upstream retries, and turn completion",
  customStandeeTitle: "Custom Character Standee",
  customStandeeDesc: "Upload a transparent PNG, WebP, or animated GIF up to 15 MB (saved offline in IndexedDB)",
  uploadStandeeBtn: "Upload Standee…",
  clearStandeeBtn: "Remove Custom",
  customStandeeActive: "Custom standee loaded",
  errInvalidType: "Please select a PNG, WebP, GIF, or JPG image",
  errTooLarge: "Standee file exceeds the 15 MB limit",
  errStorage: "Could not persist standee in local database",
};
