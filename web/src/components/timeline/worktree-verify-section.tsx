// web/src/components/timeline/worktree-verify-section.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import {
  AlertTriangle,
  Check,
  FlaskConical,
  Globe,
  LoaderCircle,
  Play,
  RefreshCw,
  Settings2,
  Square,
  Terminal,
} from "lucide-solid";
import type { WorktreeVerifyState } from "../../services/api";
import {
  fetchWorktreeVerifyState,
  openURLInInspector,
  retryWorktreeSetupScript,
  saveWorktreeVerifyCommands,
  startWorktreeVerify,
  stopWorktreeVerifyRun,
} from "../../store/workspace";
import { t } from "../../i18n";

export function WorktreeVerifySection(props: {
  sessionID?: string | null;
  directory?: string;
  onCloseMenu: () => void;
}) {
  const [state, setState] = createSignal<WorktreeVerifyState | null>(null);
  const [loading, setLoading] = createSignal(false);
  const [actionBusy, setActionBusy] = createSignal(false);
  const [configOpen, setConfigOpen] = createSignal(false);
  const [showOutput, setShowOutput] = createSignal(false);
  const [runCmd, setRunCmd] = createSignal("");
  const [testCmd, setTestCmd] = createSignal("");
  const [errMsg, setErrMsg] = createSignal<string | null>(null);
  let requestGen = 0;

  onCleanup(() => {
    requestGen++;
  });

  const effectiveSessionID = () => props.sessionID || "__root__";

  const fetchVerifyState = async (populateInputs = false) => {
    const sid = effectiveSessionID();
    const dir = props.directory;
    const gen = ++requestGen;
    if (populateInputs) setLoading(true);
    try {
      const res = await fetchWorktreeVerifyState(sid, dir);
      if (gen !== requestGen) return;
      setState(res);
      if (populateInputs) {
        setRunCmd(res.runCommand || "");
        setTestCmd(res.testCommand || "");
      }
      if (res.output && (res.runStatus === "running" || res.runStatus === "failed")) {
        setShowOutput(true);
      }
    } catch {
      /* ignore */
    } finally {
      if (gen === requestGen && populateInputs) {
        setLoading(false);
      }
    }
  };

  createEffect(() => {
    void props.sessionID;
    void props.directory;
    void fetchVerifyState(true);
  });

  createEffect(() => {
    const st = state();
    const isRunning = st?.runStatus === "running" || st?.setupStatus === "running";
    if (!isRunning) return;
    const timer = setInterval(() => {
      void fetchVerifyState(false);
    }, 1200);
    onCleanup(() => clearInterval(timer));
  });

  const handleRetrySetup = async () => {
    if (actionBusy()) return;
    setActionBusy(true);
    setErrMsg(null);
    try {
      setState(await retryWorktreeSetupScript(effectiveSessionID(), props.directory, props.sessionID));
    } catch (err: any) {
      setErrMsg(err?.message || String(err));
    } finally {
      setActionBusy(false);
    }
  };

  const handleSaveConfig = async () => {
    if (actionBusy()) return;
    setActionBusy(true);
    setErrMsg(null);
    try {
      const cfg = await saveWorktreeVerifyCommands({
        directory: props.directory,
        runCommand: runCmd().trim(),
        testCommand: testCmd().trim(),
      });
      setRunCmd(cfg.runCommand || "");
      setTestCmd(cfg.testCommand || "");
      setConfigOpen(false);
      await fetchVerifyState(false);
    } catch (err: any) {
      setErrMsg(err?.message || String(err));
    } finally {
      setActionBusy(false);
    }
  };

  const handleStartVerify = async (kind: "run" | "test") => {
    if (actionBusy()) return;
    const cmd = (kind === "run" ? runCmd() : testCmd()).trim() ||
      (kind === "run" ? state()?.runCommand : state()?.testCommand) || "";
    if (!cmd) {
      setConfigOpen(true);
      return;
    }
    setActionBusy(true);
    setErrMsg(null);
    setShowOutput(true);
    try {
      const res = await startWorktreeVerify({
        sessionID: effectiveSessionID(),
        directory: props.directory,
        kind,
        command: cmd,
      });
      setState(res);
    } catch (err: any) {
      setErrMsg(err?.message || String(err));
    } finally {
      setActionBusy(false);
    }
  };

  const handleStopVerify = async () => {
    if (actionBusy()) return;
    setActionBusy(true);
    setErrMsg(null);
    try {
      const res = await stopWorktreeVerifyRun(effectiveSessionID(), props.directory);
      setState(res);
    } catch (err: any) {
      setErrMsg(err?.message || String(err));
    } finally {
      setActionBusy(false);
    }
  };

  return (
    <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)] px-1 space-y-1.5">
      {/* 1. Worktree Setup Script Status & Retry */}
      <Show when={state()?.setupStatus && state()?.setupStatus !== "none"}>
        <div
          class={`rounded-xl border px-2.5 py-1.5 text-[11px] space-y-1 ${
            state()?.setupStatus === "failed"
              ? "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
              : state()?.setupStatus === "running"
                ? "border-amber-500/30 bg-amber-500/10 text-amber-500"
                : "border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 text-[var(--text-secondary)]"
          }`}
        >
          <div class="flex items-center justify-between gap-2">
            <span class="inline-flex items-center gap-1.5 font-medium truncate">
              <Show
                when={state()?.setupStatus === "failed"}
                fallback={
                  <Show
                    when={state()?.setupStatus === "running"}
                    fallback={<Check size={12} class="text-emerald-500 shrink-0" />}
                  >
                    <LoaderCircle size={12} class="animate-spin shrink-0" />
                  </Show>
                }
              >
                <AlertTriangle size={12} class="shrink-0" />
              </Show>
              <span class="truncate">
                {state()?.setupStatus === "failed"
                  ? t("timeline.worktreeSetupFailed")
                  : state()?.setupStatus === "running"
                    ? t("timeline.worktreeSetupRunning")
                    : t("timeline.worktreeSetupSucceeded")}
              </span>
            </span>

            <button
              type="button"
              disabled={actionBusy() || state()?.setupStatus === "running"}
              onClick={() => void handleRetrySetup()}
              class="shrink-0 inline-flex items-center gap-1 rounded-md bg-[var(--bg-panel)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer"
            >
              <RefreshCw size={10} />
              <span>{t("timeline.worktreeSetupRetry")}</span>
            </button>
          </div>

          <Show when={state()?.setupStatus === "failed" && (state()?.setupError || state()?.setupOutput)}>
            <pre class="max-h-20 overflow-y-auto rounded bg-black/20 p-1.5 font-mono text-[11px] leading-snug whitespace-pre-wrap break-all">
              {(state()?.setupOutput || state()?.setupError || "").trim().slice(-400)}
            </pre>
          </Show>
        </div>
      </Show>

      {/* 2. Run / Test / Verify Header & Controls */}
      <div class="flex items-center justify-between gap-1.5 px-0.5">
        <div class="flex items-center gap-1.5 min-w-0">
          <span class="text-[11px] font-semibold text-[var(--text-secondary)] truncate">
            {t("timeline.verifySectionTitle")}
          </span>
          <Show when={(state()?.allocatedPort ?? 0) > 0}>
            <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
              :{state()?.allocatedPort}
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-1">
          <Show when={state()?.output}>
            <button
              type="button"
              onClick={() => setShowOutput((v) => !v)}
              title={t("timeline.verifyToggleOutput")}
              class={`rounded-md p-1 transition-colors cursor-pointer ${
                showOutput()
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Terminal size={12} />
            </button>
          </Show>
          <button
            type="button"
            onClick={() => setConfigOpen((v) => !v)}
            title={t("timeline.verifyConfigureTooltip")}
            class={`rounded-md p-1 transition-colors cursor-pointer ${
              configOpen()
                ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Settings2 size={12} />
          </button>
        </div>
      </div>

      {/* 3. One-Click Run / Test / Stop Buttons */}
      <div class="flex items-center gap-1.5">
        <button
          type="button"
          disabled={actionBusy() || loading() || state()?.runStatus === "running"}
          onClick={() => void handleStartVerify("run")}
          title={runCmd() || state()?.runCommand || t("timeline.verifyConfigureTooltip")}
          class="flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--text-primary)] disabled:opacity-45 transition-colors cursor-pointer"
        >
          <Show
            when={!(state()?.runStatus === "running" && state()?.activeKind === "run")}
            fallback={<LoaderCircle size={12} class="animate-spin text-[var(--accent-primary)]" />}
          >
            <Play size={12} class="text-emerald-500" />
          </Show>
          <span>{t("timeline.verifyRunBtn")}</span>
        </button>

        <button
          type="button"
          disabled={actionBusy() || loading() || state()?.runStatus === "running"}
          onClick={() => void handleStartVerify("test")}
          title={testCmd() || state()?.testCommand || t("timeline.verifyConfigureTooltip")}
          class="flex-1 inline-flex items-center justify-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--text-primary)] disabled:opacity-45 transition-colors cursor-pointer"
        >
          <Show
            when={!(state()?.runStatus === "running" && state()?.activeKind === "test")}
            fallback={<LoaderCircle size={12} class="animate-spin text-[var(--accent-primary)]" />}
          >
            <FlaskConical size={12} class="text-[var(--accent-primary)]" />
          </Show>
          <span>{t("timeline.verifyTestBtn")}</span>
        </button>

        <Show when={state()?.runStatus === "running"}>
          <button
            type="button"
            disabled={actionBusy()}
            onClick={() => void handleStopVerify()}
            title={t("timeline.verifyStopBtn")}
            class="shrink-0 inline-flex items-center gap-1 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
          >
            <Square size={10} class="fill-current" />
            <span>{t("timeline.verifyStopBtn")}</span>
          </button>
        </Show>
      </div>

      {/* 4. Configurable Run & Test Commands */}
      <Show when={configOpen()}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 p-2 space-y-1.5">
          <div>
            <label class="block text-[11px] font-medium text-[var(--text-muted)] mb-0.5">
              {t("timeline.verifyRunCommandLabel")}
            </label>
            <input
              type="text"
              value={runCmd()}
              onInput={(e) => setRunCmd(e.currentTarget.value)}
              placeholder="npm run dev"
              class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
          </div>
          <div>
            <label class="block text-[11px] font-medium text-[var(--text-muted)] mb-0.5">
              {t("timeline.verifyTestCommandLabel")}
            </label>
            <input
              type="text"
              value={testCmd()}
              onInput={(e) => setTestCmd(e.currentTarget.value)}
              placeholder="npm test"
              class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
          </div>
          <div class="flex justify-end gap-1.5 pt-0.5">
            <button
              type="button"
              disabled={actionBusy()}
              onClick={() => void handleSaveConfig()}
              class="rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
            >
              {t("common.save")}
            </button>
          </div>
        </div>
      </Show>

      {/* 5. Preview URL One-Click Browser Inspector */}
      <Show when={state()?.previewURL}>
        {(url) => (
          <button
            type="button"
            onClick={() => {
              openURLInInspector(url());
              props.onCloseMenu();
            }}
            class="w-full flex items-center justify-between gap-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/70 hover:bg-[var(--accent-surface)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--accent-text)] transition-colors cursor-pointer"
          >
            <span class="inline-flex items-center gap-1.5 min-w-0 truncate">
              <Globe size={12} class="shrink-0 text-[var(--accent-primary)]" />
              <span class="truncate">{t("timeline.verifyOpenPreview")}</span>
            </span>
            <span class="font-mono text-[11px] opacity-80 truncate max-w-[140px]">
              {url()}
            </span>
          </button>
        )}
      </Show>

      <Show when={errMsg()}>
        <div class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2 py-1 text-[11px] text-[var(--danger-text)] break-words">
          {errMsg()}
        </div>
      </Show>

      {/* 6. Live Process Output Tail */}
      <Show when={showOutput() && state()?.output}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[#0b0d12] p-2 space-y-1">
          <div class="flex items-center justify-between text-[11px] font-mono text-gray-400">
            <span>
              {state()?.activeKind === "test"
                ? t("timeline.verifyTestBtn")
                : t("timeline.verifyRunBtn")}
            </span>
            <span
              class={
                state()?.runStatus === "failed"
                  ? "text-rose-400"
                  : state()?.runStatus === "succeeded"
                    ? "text-emerald-400"
                    : "text-amber-400"
              }
            >
              {state()?.runStatus}
            </span>
          </div>
          <pre class="max-h-28 overflow-y-auto font-mono text-[11px] leading-relaxed text-gray-200 whitespace-pre-wrap break-all selectable-text">
            {(state()?.output || "").trim().slice(-1500)}
          </pre>
        </div>
      </Show>
    </div>
  );
}
