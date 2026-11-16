import { createEffect, createSignal, For, Show } from "solid-js";
import type { createBrowserWorkflows } from "../../store/workspace";
import { t } from "../../i18n";

interface Props {
  workflows: ReturnType<typeof createBrowserWorkflows>;
  url: string;
}

export default function BrowserWorkflows(props: Props) {
  const [expanded, setExpanded] = createSignal(false);
  const [prompt, setPrompt] = createSignal("");
  const [dialogID, setDialogID] = createSignal("");
  const state = () => props.workflows.state();
  const busy = () => props.workflows.busy();
  const button = "rounded border border-[var(--border-subtle)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer";
  createEffect(() => {
    const dialog = state().dialog;
    if (dialog?.id !== dialogID()) { setDialogID(dialog?.id || ""); setPrompt(dialog?.default || ""); }
  });
  const size = (bytes: number) => bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MiB` : `${Math.ceil(bytes / 1024)} KiB`;

  return <div class="shrink-0 text-xs">
    <Show when={props.workflows.error()}><p role="status" class="px-2 py-1 text-[var(--text-muted)]">{t("inspector.browser.workflowUnavailable")}</p></Show>
    <Show when={state().upload}>{upload => <div class="border-b border-[var(--border-subtle)] bg-[var(--accent-surface)] px-2 py-2 space-y-2">
      <p role="status">{t("inspector.browser.uploadRequested")}</p>
      <p class="break-all text-[11px] text-[var(--text-muted)]">{t("inspector.browser.uploadDestination", { url: props.url })}</p>
      <label class={`${button} inline-flex items-center gap-1`}>
        {t(busy() === upload().id ? "inspector.browser.uploading" : "inspector.browser.chooseFiles")}
        <input class="sr-only" type="file" multiple={upload().multiple} disabled={Boolean(busy())}
          aria-label={t("inspector.browser.chooseFiles")} onChange={event => {
            const files = Array.from(event.currentTarget.files || []);
            event.currentTarget.value = "";
            void props.workflows.upload(files);
          }} />
      </label>
      <button class={`${button} ml-2`} disabled={Boolean(busy())} onClick={props.workflows.cancelUpload}>{t("inspector.browser.cancel")}</button>
    </div>}</Show>
    <Show when={state().dialog}>{dialog => <div role="dialog" aria-label={t("inspector.browser.pageDialog")} class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-2 space-y-2">
      <p class="font-medium">{t("inspector.browser.pageDialog")}</p>
      <p class="whitespace-pre-wrap break-words max-h-28 overflow-y-auto selectable-text">{dialog().message}</p>
      <Show when={dialog().type === "prompt"}><input class="w-full rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1"
        aria-label={t("inspector.browser.promptAnswer")} value={prompt()} maxlength={4096} onInput={event => setPrompt(event.currentTarget.value)} onKeyDown={event => event.stopPropagation()} /></Show>
      <div class="flex gap-2"><button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.answer(true, prompt())}>{t("inspector.browser.confirm")}</button>
        <Show when={dialog().type !== "alert"}><button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.answer(false, "")}>{t("inspector.browser.cancel")}</button></Show></div>
    </div>}</Show>
    <Show when={state().downloads.length || state().error}>
      <div class="border-b border-[var(--border-subtle)]">
        <button class="w-full px-2 py-1 text-left hover:bg-[var(--bg-hover)] cursor-pointer" aria-expanded={expanded()} onClick={() => setExpanded(value => !value)}>
          {t("inspector.browser.downloadCount", { count: state().downloads.length })} {expanded() ? "▾" : "▸"}
        </button>
        <Show when={expanded()}><div class="max-h-52 overflow-y-auto px-2 pb-2 space-y-2">
          <p class="text-[11px] text-[var(--text-muted)]">{t("inspector.browser.downloadTemporary")}</p>
          <Show when={state().error}><p role="alert">{t("inspector.browser.downloadLimit")}</p></Show>
          <For each={state().downloads}>{download => <div class="rounded border border-[var(--border-subtle)] p-2 space-y-1">
            <p class="truncate selectable-text" title={download.filename}>{download.filename}</p>
            <p class="text-[11px] text-[var(--text-muted)]">{t(download.error ? "inspector.browser.downloadLimit" : download.status === "completed" ? "inspector.browser.downloadComplete" : download.status === "canceled" ? "inspector.browser.downloadCanceled" : "inspector.browser.downloading")} · {size(download.received)}<Show when={download.total > 0}> / {size(download.total)}</Show></p>
            <Show when={download.status === "inProgress" && download.total > 0}><progress class="w-full h-1" max={download.total} value={download.received} aria-label={t("inspector.browser.downloading")} /></Show>
            <Show when={download.saved}><p class="break-all text-[11px] selectable-text">{download.saved}</p></Show>
            <div class="flex flex-wrap gap-1">
              <Show when={download.status === "inProgress"}><button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.download("cancel", download.id)}>{t("inspector.browser.cancel")}</button></Show>
              <Show when={download.status === "completed"}>
                <button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.download("save", download.id)}>{t("inspector.browser.saveDownload")}</button>
                <Show when={download.saved}>
                  <Show when={download.canOpen}><button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.download("open", download.id)}>{t("inspector.browser.openDownload")}</button></Show>
                  <button class={button} disabled={Boolean(busy())} onClick={() => void props.workflows.download("reveal", download.id)}>{t("inspector.browser.revealDownload")}</button>
                </Show>
              </Show>
            </div>
          </div>}</For>
        </div></Show>
      </div>
    </Show>
  </div>;
}
