// web/src/store/workspace.ts
// Flat barrel for the store layer. State lives in ./workspace-state and each
// domain module owns its own actions; this file only re-exports them so that
// components can import everything from "store/workspace".
export * from "./workspace-state";
export * from "./layout";
export * from "./inspector";
export * from "./session-query";
export * from "./session-index";
export * from "./workspace-folders";
export * from "./global-config";
export * from "./workspace-providers";
export * from "./provider-catalog";
export * from "./model-limits";
export * from "./session-ai";
export * from "./workspace-projects";
export * from "./workspace-session-lists";
export * from "./workspace-sessions";
export * from "./session-diffs";
export * from "./workspace-session-messages";
export * from "./prompt-queue";
export * from "./subagent-sessions";
export * from "./session-revert";
export * from "./session-stream";
export * from "./session-sandbox";
export * from "./session-status-sync";
export * from "./session-title";
export * from "./session-goal-state";
export * from "./session-goal";
export * from "./git-commit";
export * from "./git-attribution";
export * from "./approval-mode";
export * from "./provider-telemetry";
export * from "./provider-health";
export * from "./provider-audit";
export * from "./settings";
export * from "./daily-pulse";
export * from "./model-pricing";
export * from "./usage-history";
export * from "./request-log";
export * from "./auth-state";
export * from "./handoff";
export * from "./updater-state";
export * from "./leaderboard-state";
export * from "./prompt-history";
export * from "./diagnostics";
export * from "./file-links";
export * from "./message-lookup";
export * from "./question-answers";
export * from "./auto-models";
export * from "./auto-breaker";
export * from "./auto-candidate-status";
export * from "./skills";
export * from "./terminal-context";
export * from "./telemetry";
export * from "./session-capabilities";
export * from "./session-transcripts";
export * from "./orchestration";
export * from "./task-progress";
export * from "./task-overview";
export * from "./command-sandbox";
export * from "./code-index";
export * from "./browser";
export * from "./browser-input";
export * from "./remote-tunnel";
export * from "./pairing";
export * from "./cloudflared";
export * from "./relay";
export * from "./deep-link";
export * from "./subagent-config";
export * from "./image-viewer";



