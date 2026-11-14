import { createSignal, For, Show } from "solid-js";
import { t } from "../../i18n";
import type { CatalogBrowserSyncController } from "../../services/work-catalog";
import {
  catalogProductText,
  catalogDisplayImages,
  closeCatalogDetail,
  executeCatalogDraftAction,
  openCatalogEditor,
  workCatalogState,
} from "../../store/work-catalog";
import CatalogDraftEditor from "./CatalogDraftEditor";
import { catalogDraftStatusLabel } from "./catalog-labels";

export default function CatalogProductDetail(props: {
  onCreateTask?: () => void;
  browserController?: CatalogBrowserSyncController;
}) {
  const [copied, setCopied] = createSignal(false);

  const copyDraft = async () => {
    const draft = workCatalogState.draft;
    if (!draft) return;
    const subject =
      draft.patch.subject || catalogProductText(draft.base, "subject");
    const desc =
      draft.patch.description || catalogProductText(draft.base, "description");
    const text = desc ? `${subject}\n\n${desc}` : subject;
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* ignore clipboard unavailability */
    }
  };

  return (
    <aside
      class="w-full shrink-0 border-t border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 xl:w-[420px] xl:border-l xl:border-t-0 overflow-y-auto"
      data-testid="catalog-detail"
    >
      <Show
        when={!workCatalogState.detailLoading}
        fallback={
          <p class="text-sm text-[var(--text-muted)]">
            {t("workCatalog.loading")}
          </p>
        }
      >
        <Show when={!workCatalogState.editor} fallback={<CatalogDraftEditor />}>
          <div class="flex justify-between gap-3 mb-4">
            <h3 class="font-semibold">
              {workCatalogState.draft
                ? t("workCatalog.draft")
                : t("workCatalog.details")}
            </h3>
            <button
              onClick={closeCatalogDetail}
              class="text-xs text-[var(--text-muted)]"
            >
              {t("workCatalog.close")}
            </button>
          </div>
          <Show when={workCatalogState.product}>
            <h4 class="font-semibold break-words">
              {catalogProductText(workCatalogState.product, "subject")}
            </h4>
            <div class="my-3 flex gap-2 overflow-x-auto">
              <For each={catalogDisplayImages(workCatalogState.product)}>
                {(image) => (
                  <img
                    src={image}
                    alt=""
                    loading="lazy"
                    referrerpolicy="no-referrer"
                    class="h-20 w-20 shrink-0 rounded-lg object-cover"
                    />
                )}
              </For>
            </div>
            <dl class="my-3 space-y-2 text-xs">
              <div>
                <dt class="text-[var(--text-muted)]">
                  {t("workCatalog.productId")}
                </dt>
                <dd class="font-mono">{workCatalogState.product?.id}</dd>
              </div>
              <div>
                <dt class="text-[var(--text-muted)]">
                  {t("workCatalog.platformStatus")}
                </dt>
                <dd>
                  {catalogProductText(workCatalogState.product, "status") ||
                    t("workCatalog.unknown")}
                </dd>
              </div>
              <div>
                <dt class="text-[var(--text-muted)]">
                  {t("workCatalog.category")}
                </dt>
                <dd>
                  {catalogProductText(
                    workCatalogState.product,
                    "categoryName",
                  ) || t("workCatalog.unknown")}
                </dd>
              </div>
              <div>
                <dt class="text-[var(--text-muted)]">
                  {t("workCatalog.syncedAt")}
                </dt>
                <dd>{workCatalogState.product?.syncedAt}</dd>
              </div>
            </dl>
            <div class="flex flex-wrap gap-2">
              <button
                disabled={!workCatalogState.status?.capabilities.editDraft}
                onClick={() => openCatalogEditor("update")}
                class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
              >
                {t("workCatalog.edit")}
              </button>
              <Show when={props.browserController}>
                {(ctrl) => (
                  <button
                    type="button"
                    disabled={ctrl().busy()}
                    onClick={() =>
                      void ctrl().enrichProductDetail(
                        workCatalogState.product!.id,
                      )
                    }
                    class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50"
                  >
                    {t("workCatalog.enrichFromBrowser")}
                  </button>
                )}
              </Show>
            </div>
            <h5 class="mt-5 mb-2 text-xs font-semibold">
              {t("workCatalog.description")}
            </h5>
            <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-[var(--bg-inset)] p-3 text-xs">
              {catalogProductText(workCatalogState.product, "description")}
            </pre>
            <details class="mt-4">
              <summary class="cursor-pointer text-xs font-semibold">
                {t("workCatalog.rawData")}
              </summary>
              <pre class="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-[var(--bg-inset)] p-3 text-[11px]">
                {JSON.stringify(workCatalogState.product?.data, null, 2)}
              </pre>
            </details>
          </Show>
          <Show when={workCatalogState.draft}>
            <div class="space-y-3">
              <p class="text-xs font-semibold">
                {workCatalogState.draft?.kind === "create"
                  ? t("workCatalog.newDraft")
                  : t("workCatalog.updateDraft")}{" "}
                ·{" "}
                {catalogDraftStatusLabel(workCatalogState.draft?.status || "")}
              </p>
              <p class="font-mono text-[11px] break-all">
                {workCatalogState.draft?.id}
              </p>
              <div class="text-xs">
                <span class="text-[var(--text-muted)]">
                  {t("workCatalog.revision")}
                </span>
                <p class="mt-1 font-mono break-all">
                  {workCatalogState.draft?.revision}
                </p>
              </div>
              <Show
                when={workCatalogState.draft?.kind === "create"}
                fallback={
                  <div class="space-y-3">
                    <h4 class="text-xs font-semibold">
                      {t("workCatalog.changes")}
                    </h4>
                    <For each={workCatalogState.draft?.changes}>
                      {(change) => (
                        <div class="rounded-lg border border-[var(--border-subtle)] p-3 text-xs space-y-2">
                          <strong>{change.field}</strong>
                          <div>
                            <span class="text-[var(--text-muted)]">
                              {t("workCatalog.before")}
                            </span>
                            <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-words bg-red-500/5 p-2">
                              {typeof change.before === "string"
                                ? change.before
                                : JSON.stringify(change.before, null, 2)}
                            </pre>
                          </div>
                          <div>
                            <span class="text-[var(--text-muted)]">
                              {t("workCatalog.after")}
                            </span>
                            <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-words bg-emerald-500/5 p-2">
                              {typeof change.after === "string"
                                ? change.after
                                : JSON.stringify(change.after, null, 2)}
                            </pre>
                          </div>
                        </div>
                      )}
                    </For>
                  </div>
                }
              >
                <p class="text-xs text-[var(--text-muted)]">
                  {t("workCatalog.createUnsupported")}
                </p>
                <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-words bg-[var(--bg-inset)] p-3 text-xs">
                  {JSON.stringify(workCatalogState.draft?.newProduct, null, 2)}
                </pre>
              </Show>
              <Show when={workCatalogState.draft?.receipt}>
                <div class="rounded-lg bg-[var(--bg-inset)] p-3 text-xs">
                  <strong>{t("workCatalog.publicationMessage")}</strong>
                  <p class="mt-1">
                    {catalogDraftStatusLabel(
                      workCatalogState.draft?.receipt?.status || "",
                    )}
                  </p>
                  <p class="mt-1 text-[var(--text-muted)]">
                    {workCatalogState.draft?.receipt?.checkedAt}
                  </p>
                </div>
              </Show>
              <Show
                when={
                  workCatalogState.draft?.status === "draft" &&
                  workCatalogState.draft?.kind !== "create"
                }
              >
                <Show
                  when={
                    workCatalogState.status?.capabilities.publishUpdate !==
                    false
                  }
                  fallback={
                    <div class="space-y-2">
                      <p class="text-xs text-[var(--text-muted)]">
                        {t("workCatalog.browserPublishHint")}
                      </p>
                      <div class="flex flex-wrap gap-2">
                        <Show when={props.browserController}>
                          {(ctrl) => (
                            <button
                              type="button"
                              disabled={ctrl().busy()}
                              onClick={() =>
                                void ctrl().openDraftInSellerEditor(
                                  workCatalogState.draft!,
                                )
                              }
                              class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
                            >
                              {t("workCatalog.openEditInBrowser")}
                            </button>
                          )}
                        </Show>
                        <button
                          type="button"
                          onClick={() => void copyDraft()}
                          class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs hover:bg-[var(--bg-hover)]"
                        >
                          {copied()
                            ? t("workCatalog.copiedDraftContent")
                            : t("workCatalog.copyDraftContent")}
                        </button>
                      </div>
                    </div>
                  }
                >
                  <p class="text-xs text-[var(--text-muted)]">
                    {t("workCatalog.publicationHint")}
                  </p>
                  <Show
                    when={workCatalogState.publishContext}
                    fallback={
                      <div class="space-y-2">
                        <p class="text-xs text-amber-600">
                          {t("workCatalog.sessionRequired")}
                        </p>
                        <Show when={props.onCreateTask}>
                          <button
                            onClick={() => props.onCreateTask?.()}
                            class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs"
                          >
                            {t("workCatalog.createTask")}
                          </button>
                        </Show>
                      </div>
                    }
                  >
                    <button
                      disabled={
                        workCatalogState.publishing ||
                        !workCatalogState.status?.authorization.configured
                      }
                      onClick={() => void executeCatalogDraftAction("publish")}
                      class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
                    >
                      {t("workCatalog.publish")}
                    </button>
                  </Show>
                </Show>
              </Show>
              <Show
                when={["publishing", "unverified", "published"].includes(
                  workCatalogState.draft?.status || "",
                )}
              >
                <button
                  disabled={workCatalogState.publishing}
                  onClick={() => void executeCatalogDraftAction("verify")}
                  class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs disabled:opacity-50"
                >
                  {t("workCatalog.verify")}
                </button>
              </Show>
              <Show
                when={["draft", "conflict"].includes(
                  workCatalogState.draft?.status || "",
                )}
              >
                <button
                  disabled={workCatalogState.publishing}
                  onClick={() => void executeCatalogDraftAction("discard")}
                  class="ml-2 rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs disabled:opacity-50"
                >
                  {t("workCatalog.discard")}
                </button>
              </Show>
              <Show when={workCatalogState.publishing}>
                <p class="text-xs text-[var(--text-muted)]" role="status">
                  {t("workCatalog.approvalWaiting")}
                </p>
              </Show>
            </div>
          </Show>
        </Show>
      </Show>
    </aside>
  );
}
