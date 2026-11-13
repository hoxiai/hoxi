import {
  createEffect,
  For,
  onCleanup,
  Show,
  untrack,
  type JSX,
} from "solid-js";
import {
  Archive,
  Boxes,
  FilePlus2,
  KeyRound,
  RefreshCw,
  Search,
} from "lucide-solid";
import { t } from "../../i18n";
import type { CatalogPublishContext } from "../../services/work-catalog";
import {
  activateWorkCatalogScope,
  catalogPriceLabel,
  catalogPendingApprovals,
  importCatalogDraft,
  loadCatalogDrafts,
  loadCatalogProducts,
  observeWorkCatalog,
  openCatalogEditor,
  refreshCatalogStatus,
  selectCatalogDraft,
  selectCatalogProduct,
  setCatalogAuthorizationOpen,
  setCatalogPublishContext,
  setCatalogTab,
  startCatalogSync,
  stopCatalogSync,
  updateCatalogFilters,
  workCatalogState,
  type CatalogTab,
} from "../../store/work-catalog";
import CatalogAuthorization from "./CatalogAuthorization";
import ApprovalGateCard from "../approval/ApprovalGateCard";
import CatalogProductDetail from "./CatalogProductDetail";
import {
  catalogDraftStatusLabel,
  catalogPlatformStatusLabel,
} from "./catalog-labels";

const button =
  "inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs font-medium hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed";

export default function WorkCatalogView(props: {
  storeId?: string;
  storeName?: string;
  platform?: string;
  publishContext?: CatalogPublishContext | null;
  onCreateTask?: () => void;
  renderSources?: () => JSX.Element;
}) {
  createEffect(() => {
    const scope = props.storeId
      ? {
          storeId: props.storeId,
          storeName: props.storeName || props.storeId,
          platform: props.platform || "",
        }
      : null;
    untrack(() => activateWorkCatalogScope(scope));
    if (scope) onCleanup(untrack(observeWorkCatalog));
  });
  createEffect(() => setCatalogPublishContext(props.publishContext || null));
  const tabs = (): { id: CatalogTab; label: string }[] => [
    { id: "products", label: t("workCatalog.products") },
    { id: "drafts", label: t("workCatalog.drafts") },
    { id: "history", label: t("workCatalog.history") },
    { id: "sources", label: t("workCatalog.sources") },
  ];
  const jobLabel = () => {
    switch (workCatalogState.status?.job?.status) {
      case "running":
        return t("workCatalog.syncing", {
          fetched: workCatalogState.status?.job?.fetched || 0,
          total: workCatalogState.status?.job?.total || 0,
        });
      case "completed":
        return t("workCatalog.syncCompleted");
      case "error":
        return t("workCatalog.syncError");
      case "cancelled":
        return t("workCatalog.syncCancelled");
      case "interrupted":
        return t("workCatalog.syncInterrupted");
      default:
        return "";
    }
  };
  const authLabel = () =>
    workCatalogState.status?.authorization.verifiedRead
      ? t("workCatalog.verified")
      : workCatalogState.status?.authorization.configured
        ? t("workCatalog.configured")
        : t("workCatalog.unconfigured");
  const draftRows = () =>
    workCatalogState.drafts.filter((draft) =>
      workCatalogState.tab === "history"
        ? !!draft.receipt
        : !draft.receipt || draft.status === "conflict",
    );
  const hasDetail = () =>
    !!(
      workCatalogState.product ||
      workCatalogState.draft ||
      workCatalogState.editor ||
      workCatalogState.detailLoading
    );
  return (
    <div
      class="flex h-full min-h-0 flex-col text-[var(--text-primary)]"
      data-testid="work-catalog"
    >
      <header class="shrink-0 max-h-[70%] overflow-y-auto border-b border-[var(--border-subtle)] p-4 space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="flex items-center gap-2 text-base font-semibold">
              <Boxes size={18} />
              {t("workCatalog.title")}
            </h2>
            <p class="mt-1 text-xs text-[var(--text-muted)]">
              {props.storeId
                ? t("workCatalog.storeLabel", {
                    name: props.storeName || props.storeId,
                  })
                : t("workCatalog.noStore")}
            </p>
          </div>
          <Show when={props.storeId}>
            <div class="flex flex-wrap gap-2">
              <button
                class={button}
                disabled={!workCatalogState.status?.capabilities.supported}
                onClick={() =>
                  setCatalogAuthorizationOpen(
                    !workCatalogState.authorizationOpen,
                  )
                }
              >
                <KeyRound size={13} />
                {t("workCatalog.authorize")}
              </button>
              <button
                class={button}
                disabled={
                  !workCatalogState.status?.capabilities.sync ||
                  !workCatalogState.status?.authorization.configured ||
                  workCatalogState.status?.job?.status === "running"
                }
                onClick={() => void startCatalogSync()}
              >
                <RefreshCw
                  size={13}
                  class={
                    workCatalogState.status?.job?.status === "running"
                      ? "animate-spin"
                      : ""
                  }
                />
                {["interrupted", "error", "cancelled"].includes(
                  workCatalogState.status?.job?.status || "",
                )
                  ? t("workCatalog.resume")
                  : t("workCatalog.sync")}
              </button>
              <button
                class={`${button} bg-[var(--accent-primary)] !text-white`}
                disabled={
                  !workCatalogState.status?.capabilities.newDraft ||
                  !workCatalogState.status?.authorization.configured
                }
                onClick={() => openCatalogEditor("create")}
              >
                <FilePlus2 size={13} />
                {t("workCatalog.newProduct")}
              </button>
            </div>
          </Show>
        </div>
        <Show when={workCatalogState.status}>
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--text-muted)]">
            <span>{authLabel()}</span>
            <span>
              {t("workCatalog.count", {
                count: workCatalogState.status?.productCount || 0,
              })}
            </span>
            <span>
              {workCatalogState.status?.lastSync?.finishedAt
                ? t("workCatalog.lastSync", {
                    time: new Date(
                      workCatalogState.status!.lastSync!.finishedAt!,
                    ).toLocaleString(),
                  })
                : t("workCatalog.noSync")}
            </span>
            <button
              class="hover:text-[var(--text-primary)]"
              onClick={() => {
                void refreshCatalogStatus();
                void loadCatalogProducts();
                void loadCatalogDrafts();
              }}
            >
              {t("workCatalog.refresh")}
            </button>
          </div>
        </Show>
        <Show when={workCatalogState.status?.job}>
          <div class="space-y-2 text-xs" role="status">
            <div class="flex justify-between gap-3">
              <span>{jobLabel()}</span>
              <Show
                when={workCatalogState.status?.job?.status === "running"}
                fallback={
                  <button
                    class="text-[var(--text-muted)]"
                    onClick={() => void startCatalogSync(true)}
                    disabled={
                      !workCatalogState.status?.authorization.configured
                    }
                  >
                    {t("workCatalog.restartSync")}
                  </button>
                }
              >
                <button onClick={() => void stopCatalogSync()}>
                  {t("workCatalog.stop")}
                </button>
              </Show>
            </div>
            <Show when={workCatalogState.status?.job?.status === "running"}>
              <progress
                class="h-1.5 w-full accent-[var(--accent-primary)]"
                value={workCatalogState.status?.job?.fetched || 0}
                max={Math.max(workCatalogState.status?.job?.total || 1, 1)}
              />
            </Show>
          </div>
        </Show>
        <Show when={workCatalogState.authorizationOpen}>
          <CatalogAuthorization />
        </Show>
        <Show when={catalogPendingApprovals().length > 0}>
          <div class="space-y-2" data-testid="catalog-publish-approval">
            <For each={catalogPendingApprovals()}>
              {(request) => <ApprovalGateCard request={request} />}
            </For>
          </div>
        </Show>
        <Show when={workCatalogState.error}>
          <p
            role="alert"
            class="rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-600 break-words"
          >
            {workCatalogState.error}
          </p>
        </Show>
      </header>
      <Show
        when={props.storeId}
        fallback={
          <p class="p-6 text-sm text-[var(--text-muted)]">
            {t("workCatalog.noStore")}
          </p>
        }
      >
        <Show
          when={workCatalogState.status?.capabilities.supported}
          fallback={
            <p class="p-6 text-sm text-[var(--text-muted)]">
              {workCatalogState.status
                ? t("workCatalog.unsupported")
                : t("workCatalog.loading")}
            </p>
          }
        >
          <nav
            class="flex shrink-0 gap-1 border-b border-[var(--border-subtle)] px-4 pt-2"
            aria-label={t("workCatalog.title")}
          >
            <For each={tabs()}>
              {(tab) => (
                <button
                  class={`border-b-2 px-3 py-2 text-xs ${workCatalogState.tab === tab.id ? "border-[var(--accent-primary)] font-semibold text-[var(--accent-primary)]" : "border-transparent text-[var(--text-muted)]"}`}
                  onClick={() => setCatalogTab(tab.id)}
                >
                  {tab.label}
                </button>
              )}
            </For>
          </nav>
          <div class="flex min-h-0 flex-1 flex-col overflow-auto xl:flex-row xl:overflow-hidden">
            <main class="min-w-0 flex-1 overflow-auto p-4">
              <Show when={workCatalogState.tab === "products"}>
                <div class="mb-4 flex flex-wrap gap-2">
                  <label class="flex min-w-[180px] flex-1 items-center gap-2 rounded-lg border border-[var(--border-subtle)] px-3">
                    <Search size={14} />
                    <input
                      class="w-full bg-transparent py-2 text-xs outline-none"
                      placeholder={t("workCatalog.search")}
                      value={workCatalogState.query}
                      onInput={(event) =>
                        updateCatalogFilters(
                          event.currentTarget.value,
                          workCatalogState.statusFilter,
                          workCatalogState.draftOnly,
                        )
                      }
                    />
                  </label>
                  <select
                    class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 text-xs"
                    aria-label={t("workCatalog.platformStatus")}
                    value={workCatalogState.statusFilter}
                    onChange={(event) =>
                      updateCatalogFilters(
                        workCatalogState.query,
                        event.currentTarget.value,
                        workCatalogState.draftOnly,
                      )
                    }
                  >
                    <option value="">{t("workCatalog.allStatuses")}</option>
                    <For each={workCatalogState.page?.statuses || []}>
                      {(status) => (
                        <option value={status}>
                          {catalogPlatformStatusLabel(status)}
                        </option>
                      )}
                    </For>
                  </select>
                  <label class="inline-flex items-center gap-1.5 text-xs">
                    <input
                      type="checkbox"
                      checked={workCatalogState.draftOnly}
                      onChange={(event) =>
                        updateCatalogFilters(
                          workCatalogState.query,
                          workCatalogState.statusFilter,
                          event.currentTarget.checked,
                        )
                      }
                    />
                    {t("workCatalog.onlyDrafts")}
                  </label>
                </div>
                <Show
                  when={workCatalogState.page?.items.length}
                  fallback={
                    <div class="py-10 text-center text-sm text-[var(--text-muted)]">
                      <p>
                        {workCatalogState.loading
                          ? t("workCatalog.loading")
                          : t("workCatalog.empty")}
                      </p>
                      <Show when={!workCatalogState.status?.productCount}>
                        <p class="mt-2 text-xs">{t("workCatalog.emptySync")}</p>
                      </Show>
                    </div>
                  }
                >
                  <table class="w-full text-left text-xs">
                    <thead class="text-[var(--text-muted)]">
                      <tr>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.product")}
                        </th>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.price")}
                        </th>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.platformStatus")}
                        </th>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.stock")}
                        </th>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.skus")}
                        </th>
                        <th class="pb-3 font-medium">
                          {t("workCatalog.draftStatus")}
                        </th>
                        <th class="pb-3" />
                      </tr>
                    </thead>
                    <tbody>
                      <For each={workCatalogState.page?.items}>
                        {(product) => (
                          <tr class="border-t border-[var(--border-subtle)] hover:bg-[var(--bg-hover)]">
                            <td class="max-w-[260px] py-3 pr-3">
                              <Show when={product.thumbnail}>
                                <img
                                  src={product.thumbnail}
                                  alt=""
                                  loading="lazy"
                                  referrerpolicy="no-referrer"
                                  class="mr-2 mb-1 h-10 w-10 float-left rounded-lg object-cover"
                                />
                              </Show>
                              <button
                                class="text-left font-medium hover:text-[var(--accent-primary)]"
                                onClick={() =>
                                  void selectCatalogProduct(product.id)
                                }
                              >
                                {product.title || product.id}
                              </button>
                              <p class="mt-1 font-mono text-[10px] text-[var(--text-faint)]">
                                {product.id}
                              </p>
                            </td>
                            <td class="py-3 pr-2">
                              {catalogPriceLabel(
                                product.prices,
                                product.currency,
                              )}
                            </td>
                            <td class="py-3 pr-2">
                              {catalogPlatformStatusLabel(product.status)}
                            </td>
                            <td class="py-3 pr-2">
                              {product.stock == null
                                ? t("workCatalog.unknown")
                                : String(product.stock)}
                            </td>
                            <td class="py-3 pr-2">{product.skuCount}</td>
                            <td class="py-3 pr-2">
                              {product.hasDraft
                                ? t("workCatalog.hasDraft")
                                : t("workCatalog.noDraft")}
                            </td>
                            <td>
                              <button
                                class="text-[var(--accent-primary)]"
                                onClick={() =>
                                  void selectCatalogProduct(product.id)
                                }
                              >
                                {t("workCatalog.view")}
                              </button>
                            </td>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                  <p class="mt-2 text-[10px] text-[var(--text-faint)]">
                    {t("workCatalog.imagesDestination")}
                  </p>
                  <div class="mt-4 flex items-center justify-between gap-2 text-xs text-[var(--text-muted)]">
                    <span>
                      {t("workCatalog.page", {
                        from: workCatalogState.offset + 1,
                        to:
                          workCatalogState.offset +
                          (workCatalogState.page?.items.length || 0),
                        total: workCatalogState.page?.total || 0,
                      })}
                    </span>
                    <div class="flex gap-2">
                      <button
                        class={button}
                        disabled={
                          workCatalogState.loading ||
                          workCatalogState.offset === 0
                        }
                        onClick={() =>
                          void loadCatalogProducts(
                            Math.max(0, workCatalogState.offset - 20),
                          )
                        }
                      >
                        {t("workCatalog.previous")}
                      </button>
                      <button
                        class={button}
                        disabled={
                          workCatalogState.loading ||
                          !workCatalogState.page?.nextOffset
                        }
                        onClick={() =>
                          void loadCatalogProducts(
                            workCatalogState.page?.nextOffset || 0,
                          )
                        }
                      >
                        {t("workCatalog.next")}
                      </button>
                    </div>
                  </div>
                </Show>
              </Show>
              <Show
                when={
                  workCatalogState.tab === "drafts" ||
                  workCatalogState.tab === "history"
                }
              >
                <div class="space-y-2">
                  <For
                    each={draftRows()}
                    fallback={
                      <p class="py-10 text-center text-sm text-[var(--text-muted)]">
                        {workCatalogState.tab === "history"
                          ? t("workCatalog.emptyHistory")
                          : t("workCatalog.emptyDrafts")}
                      </p>
                    }
                  >
                    {(draft) => (
                      <button
                        class="flex w-full items-start justify-between gap-3 rounded-xl border border-[var(--border-subtle)] p-3 text-left hover:bg-[var(--bg-hover)]"
                        onClick={() => void selectCatalogDraft(draft.id)}
                      >
                        <div class="min-w-0">
                          <p class="truncate text-sm font-medium">
                            {draft.title || draft.productId || draft.id}
                          </p>
                          <p class="mt-1 text-[11px] text-[var(--text-muted)]">
                            {draft.kind === "create"
                              ? t("workCatalog.newDraft")
                              : t("workCatalog.updateDraft")}{" "}
                            · {new Date(draft.createdAt).toLocaleString()}
                          </p>
                        </div>
                        <span class="shrink-0 text-xs text-[var(--accent-primary)]">
                          {catalogDraftStatusLabel(draft.status)}
                        </span>
                      </button>
                    )}
                  </For>
                </div>
              </Show>
              <Show when={workCatalogState.tab === "sources"}>
                <Show
                  when={props.renderSources}
                  fallback={
                    <div class="space-y-3">
                      <Archive size={24} class="text-[var(--text-muted)]" />
                      <p class="text-sm text-[var(--text-muted)]">
                        {t("workCatalog.sourceUnavailable")}
                      </p>
                      <label class={`${button} cursor-pointer`}>
                        {t("workCatalog.importJSON")}
                        <input
                          type="file"
                          accept="application/json,.json"
                          class="hidden"
                          onChange={(event) => {
                            const file = event.currentTarget.files?.[0];
                            if (file) void importCatalogDraft(file);
                            event.currentTarget.value = "";
                          }}
                        />
                      </label>
                    </div>
                  }
                >
                  {props.renderSources?.()}
                </Show>
              </Show>
            </main>
            <Show when={hasDetail()}>
              <CatalogProductDetail onCreateTask={props.onCreateTask} />
            </Show>
          </div>
        </Show>
      </Show>
    </div>
  );
}
