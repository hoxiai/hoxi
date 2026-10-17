// web/src/i18n/locales/en-US/inspector.ts
import type { InspectorI18n } from "../../types";

export const inspector: InspectorI18n = {
  tabs: {
    diffs: "Diffs",
    memory: "Memory",
    files: "Files",
    terminal: "Terminal",
  },
  diffs: {
    empty: "No file modifications in this session.",
    emptyDesc: "Changes made by tools will show here with unified hunk diffs.",
    filesChanged: "{count} files changed",
    files: "files",
    unified: "Unified",
    split: "Split",
    refresh: "Refresh session diff",
    comment: "Comment",
    openFile: "Open file in Inspector",
    reviewNoteOn: "Review note on",
    lineNumPlaceholder: "Line # (opt)",
    commentPlaceholder:
      "Describe what to change on this file/line (Enter to queue)…",
    addToReview: "Add to Review",
    noPatch: "(no patch payload for this file)",
    revertFileTooltip: "Revert changes to this file",
    revertFileBtn: "Revert",
  },
  memory: {
    title: "8-Dimension Structured Memory",
    empty: "No memory compacted yet.",
    emptyDesc:
      "Long-term session facts, goals, constraints and fixes are tracked here.",
    goals: "Goals",
    constraints: "Constraints",
    keyFacts: "Key Facts",
    changedFiles: "Changed Files",
    errorsAndFixes: "Errors & Fixes",
    todo: "To-Do",
    progress: "Progress",
    nextSteps: "Next Steps",
    save: "Save Memory",
    notFound:
      "project.md not found yet — it is created on the first compaction.",
    reload: "Reload memory",
  },
  files: {
    searchPlaceholder: "Search workspace files (Enter)…",
    noFiles: "No files found in workspace.",
    noMatchingFiles: "No matching files.",
    noFilesInWorkspace: "No files found in workspace.",
    back: "Back",
  },
  terminal: {
    connected: "PTY connected",
    disconnected: "PTY disconnected",
    reconnect: "Reconnect terminal",
    clear: "Clear",
    connecting: "Connecting PTY…",
    error: "PTY error",
  },
};
