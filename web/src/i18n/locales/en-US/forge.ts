// web/src/i18n/locales/en-US/forge.ts
export const forge = {
  open: "Open pull request #{number}",
  tooltip: "{title}\n{state} · {checks} · {review}",
  states: {
    open: "Open",
    draft: "Draft",
    merged: "Merged",
    closed: "Closed",
  },
  review: {
    approved: "Approved",
    changesRequested: "Changes requested",
    pending: "Review pending",
    none: "No review",
  },
  checks: {
    none: "No checks",
    passed: "{passed}/{total} checks passed",
    failed: "{count} checks failing",
    pending: "{count} checks running",
  },
};
