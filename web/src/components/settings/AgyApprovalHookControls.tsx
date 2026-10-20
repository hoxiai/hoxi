// web/src/components/settings/AgyApprovalHookControls.tsx
import { createSignal, onMount, Show } from "solid-js";
import { RefreshCw, Trash2, Wrench } from "lucide-solid";
import {
  fetchAgyHookStatus,
  installAgyHook,
  uninstallAgyHook,
  type AgyHookStatus,
} from "../../store/workspace";
import { t } from "../../i18n";
import { Spinner } from "../icons";
import { SettingGroup, SettingRow } from "./SettingsModal";

type HookState = "installed" | "outdated" | "missing" | "error";

// requestJSON throws `HTTP <status>: {"error":"..."}`; show only the reason.
function errorText(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err);
  const body = raw.replace(/^HTTP \d+:\s*/, "");
  try {
    const parsed = JSON.parse(body);
    if (parsed && typeof parsed.error === "string") return parsed.error;
  } catch {
    /* not JSON */
  }
  return body || raw;
}

const buttonClass =
  "inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-default";

const stateClass: Record<HookState, { dot: string; text: string }> = {
  installed: { dot: "bg-[var(--ok)]", text: "text-[var(--ok-text)]" },
  outdated: { dot: "bg-[var(--warn)]", text: "text-[var(--warn-text)]" },
  missing: { dot: "bg-[var(--text-faint)]", text: "text-[var(--text-secondary)]" },
  error: { dot: "bg-[var(--danger-text)]", text: "text-[var(--danger-text)]" },
};

export function AgyApprovalHookControls() {
  const [status, setStatus] = createSignal<AgyHookStatus | null>(null);
  const [busy, setBusy] = createSignal<"load" | "install" | "uninstall" | null>(null);
  const [actionError, setActionError] = createSignal("");

  const state = (): HookState | null => {
    const st = status();
    if (!st) return null;
    if (st.error) return "error";
    if (!st.installed) return "missing";
    return st.current ? "installed" : "outdated";
  };

  const stateLabel = () => {
    switch (state()) {
      case "installed":
        return t("settings.agyHookInstalled");
      case "outdated":
        return t("settings.agyHookOutdated");
      case "missing":
        return t("settings.agyHookNotInstalled");
      case "error":
        return t("settings.agyHookError");
      default:
        return t("common.loading");
    }
  };

  const run = async (kind: "load" | "install" | "uninstall") => {
    if (busy()) return;
    setBusy(kind);
    setActionError("");
    try {
      const next =
        kind === "install"
          ? await installAgyHook()
          : kind === "uninstall"
            ? await uninstallAgyHook()
            : await fetchAgyHookStatus();
      setStatus(next);
    } catch (err) {
      setActionError(errorText(err));
      // A failed write still leaves hooks.json in some state worth showing.
      if (kind !== "load") {
        try {
          setStatus(await fetchAgyHookStatus());
        } catch {
          /* keep previous status */
        }
      }
    } finally {
      setBusy(null);
    }
  };

  onMount(() => {
    void run("load");
  });

  const onUninstall = () => {
    if (!confirm(t("settings.agyHookUninstallConfirm"))) return;
    void run("uninstall");
  };

  return (
    <SettingGroup
      title={t("settings.sectionAgyHookTitle")}
      tip={t("settings.sectionAgyHookDesc")}
      action={
        <button
          type="button"
          disabled={busy() !== null}
          onClick={() => void run("load")}
          class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
          title={t("common.refresh")}
        >
          <RefreshCw size={14} />
        </button>
      }
    >
      <SettingRow label={t("settings.agyHookStatusLabel")}>
        <span
          class={`inline-flex items-center gap-2 text-[13px] font-medium ${
            state() ? stateClass[state()!].text : "text-[var(--text-muted)]"
          }`}
        >
          <span class={`h-2.5 w-2.5 rounded-full ${state() ? stateClass[state()!].dot : "bg-[var(--text-faint)]"}`} />
          {stateLabel()}
        </span>
      </SettingRow>

      <SettingRow label={t("settings.agyHookPathLabel")}>
        <span
          class="max-w-[320px] truncate font-mono text-[13px] text-[var(--text-secondary)] selectable-text"
          title={status()?.path}
        >
          {status()?.path || "—"}
        </span>
      </SettingRow>

      <Show when={state() === "outdated" || state() === "error" || actionError()}>
        <div class="px-5 py-3 space-y-1 text-[12px] leading-relaxed">
          <Show when={state() === "outdated"}>
            <p class="text-[var(--warn-text)]">
              {t("settings.agyHookOutdatedHint", { executable: status()?.executable || "—" })}
            </p>
          </Show>
          <Show when={status()?.error}>
            <p class="break-all text-[var(--danger-text)] selectable-text">{status()?.error}</p>
          </Show>
          <Show when={actionError() && actionError() !== status()?.error}>
            <p class="break-all text-[var(--danger-text)] selectable-text">
              {t("settings.agyHookActionFailed", { error: actionError() })}
            </p>
          </Show>
        </div>
      </Show>

      <div class="px-5 py-3 flex flex-wrap items-center gap-2.5">
        <button
          type="button"
          disabled={busy() !== null || !!status()?.error}
          onClick={() => void run("install")}
          class={buttonClass}
        >
          <Show when={busy() === "install"} fallback={<Wrench size={13} class="text-[var(--accent-primary)]" />}>
            <Spinner class="h-3.5 w-3.5" />
          </Show>
          {status()?.installed ? t("settings.agyHookReinstallBtn") : t("settings.agyHookInstallBtn")}
        </button>
        <button
          type="button"
          disabled={busy() !== null || !status()?.installed}
          onClick={onUninstall}
          class={buttonClass}
        >
          <Show when={busy() === "uninstall"} fallback={<Trash2 size={13} />}>
            <Spinner class="h-3.5 w-3.5" />
          </Show>
          {t("settings.agyHookUninstallBtn")}
        </button>
      </div>
    </SettingGroup>
  );
}
