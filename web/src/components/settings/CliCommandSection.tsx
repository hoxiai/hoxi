// web/src/components/settings/CliCommandSection.tsx
import { createSignal, onMount, Show } from "solid-js";
import { Check, Copy, RefreshCw, Terminal, Trash2, Wrench } from "lucide-solid";
import {
  fetchCliLinkStatus,
  installCliLink,
  uninstallCliLink,
  type CliLinkStatus,
} from "../../store/workspace";
import { t } from "../../i18n";
import { Spinner } from "../icons";
import { SettingGroup, SettingRow } from "./setting-controls";
import { confirmAction } from "./confirm-dialog";

type CliVisualState =
  | "installed"
  | "external"
  | "notInPath"
  | "outdated"
  | "missing"
  | "unavailable";

function parseErrorText(err: unknown): { code?: number; message: string } {
  const raw = err instanceof Error ? err.message : String(err);
  const codeMatch = raw.match(/^HTTP (\d+):\s*/);
  const code = codeMatch ? Number(codeMatch[1]) : undefined;
  const body = raw.replace(/^HTTP \d+:\s*/, "");
  try {
    const parsed = JSON.parse(body);
    if (parsed && typeof parsed.error === "string") {
      return { code, message: parsed.error };
    }
  } catch {
    /* plain text */
  }
  return { code, message: body || raw };
}

const secondaryBtnClass =
  "inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-default";

const primaryBtnClass =
  "hoxi-press inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-3.5 py-1.5 text-[13px] font-medium text-[var(--accent-foreground)] shadow-2xs transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-default";

const statePalette: Record<CliVisualState, { dot: string; text: string }> = {
  installed: { dot: "bg-[var(--ok)]", text: "text-[var(--ok-text)]" },
  external: { dot: "bg-[var(--ok)]", text: "text-[var(--ok-text)]" },
  notInPath: { dot: "bg-[var(--warn)]", text: "text-[var(--warn-text)]" },
  outdated: { dot: "bg-[var(--warn)]", text: "text-[var(--warn-text)]" },
  missing: { dot: "bg-[var(--text-faint)]", text: "text-[var(--text-secondary)]" },
  unavailable: { dot: "bg-[var(--text-faint)]", text: "text-[var(--text-muted)]" },
};

export function CliCommandSection(props: { onToast?: (msg: string) => void }) {
  const [status, setStatus] = createSignal<CliLinkStatus | null>(null);
  const [busy, setBusy] = createSignal<"load" | "install" | "uninstall" | null>(null);
  const [actionError, setActionError] = createSignal("");
  const [hiddenRemote, setHiddenRemote] = createSignal(false);
  const [copiedHint, setCopiedHint] = createSignal(false);

  const visualState = (): CliVisualState | null => {
    const st = status();
    if (!st) return null;
    if (st.error) return "unavailable";
    if (!st.installed) return "missing";
    if (!st.current) return "outdated";
    if (st.external) return "external";
    return st.inPath ? "installed" : "notInPath";
  };

  const stateLabel = () => {
    switch (visualState()) {
      case "installed":
        return t("settings.cliInstalled");
      case "external":
        return t("settings.cliInstalledExternal");
      case "notInPath":
        return t("settings.cliInstalledNotInPath");
      case "outdated":
        return t("settings.cliOutdated");
      case "missing":
        return t("settings.cliNotInstalled");
      case "unavailable":
        return t("settings.cliUnavailable");
      default:
        return t("common.loading");
    }
  };

  const runAction = async (kind: "load" | "install" | "uninstall") => {
    if (busy()) return;
    setBusy(kind);
    setActionError("");
    try {
      const next =
        kind === "install"
          ? await installCliLink()
          : kind === "uninstall"
            ? await uninstallCliLink()
            : await fetchCliLinkStatus();
      setStatus(next);
      if (kind === "install" && next.linkPath) {
        props.onToast?.(t("settings.cliInstallSuccess", { path: next.linkPath }));
      } else if (kind === "uninstall") {
        props.onToast?.(t("settings.cliUninstallSuccess"));
      }
    } catch (err) {
      const parsed = parseErrorText(err);
      if (kind === "load" && parsed.code === 403) {
        setHiddenRemote(true);
        return;
      }
      setActionError(parsed.message);
      if (kind !== "load") {
        try {
          setStatus(await fetchCliLinkStatus());
        } catch {
          /* preserve previous status */
        }
      }
    } finally {
      setBusy(null);
    }
  };

  onMount(() => {
    void runAction("load");
  });

  const onUninstall = async () => {
    if (!(await confirmAction(t("settings.cliUninstallConfirm")))) return;
    void runAction("uninstall");
  };

  const onCopyHint = async () => {
    const hint = status()?.pathHint;
    if (!hint || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(hint);
      setCopiedHint(true);
      props.onToast?.(t("settings.cliSnippetCopied"));
      setTimeout(() => setCopiedHint(false), 2000);
    } catch {
      /* ignore clipboard error */
    }
  };

  const installBtnLabel = () => {
    const vs = visualState();
    if (vs === "outdated") return t("settings.cliUpdateBtn");
    if (vs === "installed" || vs === "notInPath") return t("settings.cliReinstallBtn");
    return t("settings.cliInstallBtn");
  };

  return (
    <Show when={!hiddenRemote()}>
      <SettingGroup
        title={t("settings.sectionCliTitle")}
        tip={t("settings.sectionCliDesc")}
        action={
          <button
            type="button"
            disabled={busy() !== null}
            onClick={() => void runAction("load")}
            class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
            title={t("common.refresh")}
          >
            <RefreshCw size={14} />
          </button>
        }
      >
        <SettingRow label={t("settings.cliStatusLabel")}>
          <span
            class={`inline-flex items-center gap-2 text-[13px] font-medium ${
              visualState() ? statePalette[visualState()!].text : "text-[var(--text-muted)]"
            }`}
          >
            <span
              class={`h-2.5 w-2.5 rounded-full ${
                visualState() ? statePalette[visualState()!].dot : "bg-[var(--text-faint)]"
              }`}
            />
            {stateLabel()}
          </span>
        </SettingRow>

        <SettingRow label={t("settings.cliLinkPathLabel")}>
          <span
            class="max-w-[340px] truncate font-mono text-[13px] text-[var(--text-secondary)] selectable-text"
            title={status()?.linkPath || status()?.suggestedDir}
          >
            {status()?.linkPath || status()?.suggestedDir || "—"}
          </span>
        </SettingRow>

        <Show
          when={
            visualState() === "outdated" ||
            visualState() === "notInPath" ||
            visualState() === "unavailable" ||
            actionError()
          }
        >
          <div class="px-5 py-3 space-y-2 text-[12px] leading-relaxed bg-[var(--bg-inset)]/40">
            <Show when={visualState() === "outdated"}>
              <p class="text-[var(--warn-text)]">
                {t("settings.cliOutdatedHint", {
                  oldTarget: status()?.targetPath || "—",
                  executable: status()?.executable || "—",
                })}
              </p>
            </Show>

            <Show when={visualState() === "notInPath" && status()?.pathHint}>
              <div class="space-y-1.5">
                <p class="text-[var(--text-secondary)]">{t("settings.cliPathHintDesc")}</p>
                <div class="flex items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2">
                  <code class="font-mono text-[11.5px] text-[var(--text-primary)] truncate selectable-text">
                    {status()?.pathHint}
                  </code>
                  <button
                    type="button"
                    onClick={() => void onCopyHint()}
                    class="inline-flex shrink-0 items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <Show when={copiedHint()} fallback={<Copy size={12} />}>
                      <Check size={12} class="text-emerald-500" />
                    </Show>
                    <span>{t("settings.cliCopySnippetBtn")}</span>
                  </button>
                </div>
              </div>
            </Show>

            <Show when={status()?.error}>
              <p class="break-all text-[var(--text-muted)] font-mono text-[11.5px] selectable-text">
                {status()?.error}
              </p>
            </Show>

            <Show when={actionError() && actionError() !== status()?.error}>
              <p class="break-all text-[var(--danger-text)] selectable-text">
                {t("settings.cliActionFailed", { error: actionError() })}
              </p>
            </Show>
          </div>
        </Show>

        <Show when={visualState() !== "external"}>
          <div class="px-5 py-3 flex flex-wrap items-center gap-2.5">
            <button
              type="button"
              disabled={busy() !== null || !!status()?.error}
              onClick={() => void runAction("install")}
              class={
                visualState() === "missing" || visualState() === "outdated"
                  ? primaryBtnClass
                  : secondaryBtnClass
              }
            >
              <Show
                when={busy() === "install"}
                fallback={
                  visualState() === "missing" || visualState() === "outdated" ? (
                    <Terminal size={13} />
                  ) : (
                    <Wrench size={13} class="text-[var(--accent-primary)]" />
                  )
                }
              >
                <Spinner class="h-3.5 w-3.5" />
              </Show>
              <span>{installBtnLabel()}</span>
            </button>

            <Show when={status()?.installed && !status()?.external}>
              <button
                type="button"
                disabled={busy() !== null}
                onClick={() => void onUninstall()}
                class={secondaryBtnClass}
              >
                <Show when={busy() === "uninstall"} fallback={<Trash2 size={13} />}>
                  <Spinner class="h-3.5 w-3.5" />
                </Show>
                <span>{t("settings.cliUninstallBtn")}</span>
              </button>
            </Show>
          </div>
        </Show>
      </SettingGroup>
    </Show>
  );
}
