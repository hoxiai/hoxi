// web/src/components/settings/confirm-dialog.ts
// Wails' WKWebView does not implement `window.confirm`, so a bare `confirm()`
// silently returns false and every guarded destructive action becomes a no-op.
// This renders a themed in-app dialog and resolves with the user's choice.
import { t } from "../../i18n";

let closeActive: ((value: boolean) => void) | null = null;

export function confirmAction(message: string): Promise<boolean> {
  return new Promise((resolve) => {
    closeActive?.(false);

    const overlay = document.createElement("div");
    overlay.className = "fixed inset-0 z-[200] flex items-center justify-center bg-black/45 p-4";
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");

    const card = document.createElement("div");
    card.className =
      "w-full max-w-sm rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-5 shadow-2xl";

    const text = document.createElement("p");
    text.className = "text-[13px] leading-relaxed text-[var(--text-primary)] whitespace-pre-wrap";
    text.textContent = message;

    const actions = document.createElement("div");
    actions.className = "mt-5 flex justify-end gap-2";

    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className =
      "hoxi-press rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-4 py-2 text-[13px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer";
    cancel.textContent = t("common.cancel");

    const confirm = document.createElement("button");
    confirm.type = "button";
    confirm.className =
      "hoxi-press rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[13px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer";
    confirm.textContent = t("common.confirm");

    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") close(false);
      else if (event.key === "Enter") close(true);
    };

    function close(value: boolean) {
      document.removeEventListener("keydown", onKey, true);
      overlay.remove();
      if (closeActive === close) closeActive = null;
      resolve(value);
    }

    cancel.addEventListener("click", () => close(false));
    confirm.addEventListener("click", () => close(true));
    overlay.addEventListener("click", (event) => {
      if (event.target === overlay) close(false);
    });

    actions.append(cancel, confirm);
    card.append(text, actions);
    overlay.append(card);
    document.body.append(overlay);
    closeActive = close;
    document.addEventListener("keydown", onKey, true);
    confirm.focus();
  });
}
