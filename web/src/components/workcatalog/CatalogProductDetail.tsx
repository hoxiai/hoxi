import { createMemo, createSignal, For, Show } from "solid-js";
import { t } from "../../i18n";
import type {
  CatalogBrowserSyncController,
  CatalogProduct,
} from "../../services/work-catalog";
import {
  catalogDisplayImages,
  catalogPriceLabel,
  catalogProductText,
  closeCatalogDetail,
  executeCatalogDraftAction,
  openCatalogEditor,
  workCatalogState,
} from "../../store/work-catalog";
import CatalogDraftEditor from "./CatalogDraftEditor";
import { catalogDraftStatusLabel } from "./catalog-labels";

interface NormalizedTier {
  qty: string;
  priceLabel: string;
}

interface NormalizedSkuRow {
  key: string;
  spec: string;
  price: string;
  stock: string;
  code: string;
}

interface NormalizedAttrItem {
  name: string;
  value: string;
}

function asRecord(val: unknown): Record<string, unknown> | null {
  return val && typeof val === "object" && !Array.isArray(val)
    ? (val as Record<string, unknown>)
    : null;
}

function scalarText(val: unknown): string {
  if (typeof val === "string") return val.trim();
  if (typeof val === "number" && Number.isFinite(val)) return String(val);
  return "";
}

function extractSaleOverview(product: CatalogProduct | null): {
  stock: string;
  tiers: NormalizedTier[];
} {
  const sale = asRecord(product?.data.saleInfo);
  if (!sale) return { stock: "", tiers: [] };
  const stock = scalarText(sale.amountOnSale);
  const rawRanges = Array.isArray(sale.priceRanges) ? sale.priceRanges : [];
  const tiers: NormalizedTier[] = [];
  for (const r of rawRanges) {
    const rec = asRecord(r);
    if (!rec || rec.price === undefined || rec.price === null) continue;
    const qty = scalarText(rec.startQuantity || 1) || "1";
    tiers.push({
      qty,
      priceLabel: catalogPriceLabel([{ price: rec.price }]),
    });
  }
  return { stock, tiers };
}

function extractPackagingFields(
  product: CatalogProduct | null,
): Array<{ labelKey: string; value: string }> {
  const ship =
    asRecord(product?.data.shippingInfo) ||
    asRecord(product?.data.productShippingInfo);
  if (!ship) return [];

  const weight =
    scalarText(ship.weightText) ||
    (scalarText(ship.unitWeight) ? `${scalarText(ship.unitWeight)} kg` : "") ||
    (scalarText(ship.suttleWeight)
      ? `${scalarText(ship.suttleWeight)} kg`
      : "");

  const dimsParts = [
    scalarText(ship.length || ship.packageLength),
    scalarText(ship.width || ship.packageWidth),
    scalarText(ship.height || ship.packageHeight),
  ].filter(Boolean);
  const dimsFallback = dimsParts.length === 3 ? `${dimsParts.join("x")} cm` : "";
  const size =
    scalarText(ship.packageSize) ||
    dimsFallback ||
    scalarText(ship.volume);

  const rows: Array<{ labelKey: string; value: string }> = [];
  if (weight) rows.push({ labelKey: "workCatalog.unitWeight", value: weight });
  if (size) rows.push({ labelKey: "workCatalog.packageSize", value: size });
  const packingWay = scalarText(ship.packingWay);
  if (packingWay)
    rows.push({ labelKey: "workCatalog.packingWay", value: packingWay });
  const pcs = scalarText(ship.pcsPerCarton);
  if (pcs) rows.push({ labelKey: "workCatalog.pcsPerCarton", value: pcs });
  const carton = scalarText(ship.cartonSize);
  if (carton) rows.push({ labelKey: "workCatalog.cartonSize", value: carton });
  const from = scalarText(ship.sendGoodsAddressText);
  if (from) rows.push({ labelKey: "workCatalog.shipFrom", value: from });
  const handling = scalarText(
    ship.handlingTime || (ship.sendGoodsDays ? `${ship.sendGoodsDays}d` : ""),
  );
  if (handling)
    rows.push({ labelKey: "workCatalog.handlingTime", value: handling });
  return rows;
}

function extractSkuRows(product: CatalogProduct | null): NormalizedSkuRow[] {
  const list = product?.data.skuInfos;
  if (!Array.isArray(list)) return [];
  const out: NormalizedSkuRow[] = [];
  for (let i = 0; i < list.length && out.length < 100; i++) {
    const rec = asRecord(list[i]);
    if (!rec) continue;
    const attrParts = Array.isArray(rec.attributes)
      ? rec.attributes
          .map((a) => {
            const ar = asRecord(a);
            return scalarText(ar?.attributeValue ?? ar?.value);
          })
          .filter(Boolean)
      : [];
    const spec =
      scalarText(rec.specLabel) ||
      attrParts.join(" / ") ||
      scalarText(rec.cargoNumber) ||
      scalarText(rec.skuCode) ||
      scalarText(rec.skuId) ||
      String(i + 1);
    const rawPrice = rec.price ?? rec.consignPrice ?? rec.retailPrice;
    const price =
      rawPrice !== undefined && rawPrice !== null && scalarText(rawPrice) !== ""
        ? catalogPriceLabel([{ price: rawPrice }])
        : "—";
    const stock = scalarText(rec.amountOnSale) || "—";
    const code = scalarText(rec.cargoNumber || rec.skuCode) || "—";
    out.push({
      key: `${scalarText(rec.skuId) || i}-${i}`,
      spec,
      price,
      stock,
      code,
    });
  }
  return out;
}

function extractAttributeRows(
  product: CatalogProduct | null,
): NormalizedAttrItem[] {
  const list = product?.data.attributes;
  if (!Array.isArray(list)) return [];
  const out: NormalizedAttrItem[] = [];
  for (const raw of list) {
    const rec = asRecord(raw);
    if (!rec) continue;
    const name = scalarText(rec.attributeName || rec.name);
    const value = scalarText(rec.value ?? rec.attributeValue);
    if (!name || !value) continue;
    out.push({ name, value });
    if (out.length >= 60) break;
  }
  return out;
}

export default function CatalogProductDetail(props: {
  onCreateTask?: () => void;
  browserController?: CatalogBrowserSyncController;
}) {
  const [copied, setCopied] = createSignal(false);

  const saleOverview = createMemo(() =>
    extractSaleOverview(workCatalogState.product),
  );
  const packagingRows = createMemo(() =>
    extractPackagingFields(workCatalogState.product),
  );
  const skuRows = createMemo(() => extractSkuRows(workCatalogState.product));
  const attrRows = createMemo(() =>
    extractAttributeRows(workCatalogState.product),
  );
  const hasRichDetail = createMemo(
    () =>
      Boolean(
        catalogProductText(workCatalogState.product, "description").trim(),
      ) ||
      packagingRows().length > 0 ||
      attrRows().length > 0,
  );

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
      class="w-full shrink-0 border-t border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 xl:w-[440px] xl:border-l xl:border-t-0 overflow-y-auto"
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
            <dl class="my-3 grid grid-cols-2 gap-2 text-xs">
              <div>
                <dt class="text-[var(--text-muted)]">
                  {t("workCatalog.productId")}
                </dt>
                <dd class="font-mono break-all">
                  {workCatalogState.product?.id}
                </dd>
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
              <div class="col-span-2">
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
              <div class="col-span-2">
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

            <Show when={!hasRichDetail()}>
              <p class="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-2.5 text-xs text-[var(--text-secondary)]">
                {t("workCatalog.missingDetailHint")}
              </p>
            </Show>

            <Show
              when={
                saleOverview().tiers.length > 0 || Boolean(saleOverview().stock)
              }
            >
              <div class="mt-4 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 text-xs">
                <div class="flex items-center justify-between gap-2 mb-2">
                  <span class="font-semibold">
                    {t("workCatalog.priceAndStock")}
                  </span>
                  <Show when={saleOverview().stock}>
                    <span class="text-[var(--text-muted)]">
                      {t("workCatalog.stock")}: {saleOverview().stock}
                    </span>
                  </Show>
                </div>
                <Show when={saleOverview().tiers.length > 0}>
                  <div class="flex flex-wrap gap-1.5">
                    <For each={saleOverview().tiers}>
                      {(tier) => (
                        <span class="rounded bg-[var(--bg-panel)] px-2 py-1 font-mono text-[11px] border border-[var(--border-subtle)]">
                          {t("workCatalog.minOrderQty", { qty: tier.qty })} ·{" "}
                          {tier.priceLabel}
                        </span>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            </Show>

            <Show when={packagingRows().length > 0}>
              <div class="mt-4">
                <h5 class="mb-2 text-xs font-semibold">
                  {t("workCatalog.packagingSection")}
                </h5>
                <dl class="grid grid-cols-2 gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 text-xs">
                  <For each={packagingRows()}>
                    {(item) => (
                      <div>
                        <dt class="text-[var(--text-muted)]">
                          {t(item.labelKey as "workCatalog.unitWeight")}
                        </dt>
                        <dd class="font-medium break-words">{item.value}</dd>
                      </div>
                    )}
                  </For>
                </dl>
              </div>
            </Show>

            <Show when={skuRows().length > 0}>
              <div class="mt-4">
                <h5 class="mb-2 text-xs font-semibold">
                  {t("workCatalog.skuSection", { count: skuRows().length })}
                </h5>
                <div class="max-h-56 overflow-auto rounded-lg border border-[var(--border-subtle)]">
                  <table class="w-full text-left text-[11px]">
                    <thead class="sticky top-0 bg-[var(--bg-inset)] text-[var(--text-muted)]">
                      <tr>
                        <th class="px-2.5 py-1.5 font-medium">
                          {t("workCatalog.skuSpec")}
                        </th>
                        <th class="px-2 py-1.5 font-medium">
                          {t("workCatalog.skuPrice")}
                        </th>
                        <th class="px-2 py-1.5 font-medium">
                          {t("workCatalog.skuStock")}
                        </th>
                        <th class="px-2 py-1.5 font-medium">
                          {t("workCatalog.skuCode")}
                        </th>
                      </tr>
                    </thead>
                    <tbody class="divide-y divide-[var(--border-subtle)]">
                      <For each={skuRows()}>
                        {(row) => (
                          <tr>
                            <td class="px-2.5 py-1.5 break-words">
                              {row.spec}
                            </td>
                            <td class="px-2 py-1.5 font-mono whitespace-nowrap">
                              {row.price}
                            </td>
                            <td class="px-2 py-1.5 font-mono whitespace-nowrap">
                              {row.stock}
                            </td>
                            <td class="px-2 py-1.5 font-mono text-[var(--text-muted)] break-all">
                              {row.code}
                            </td>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                </div>
              </div>
            </Show>

            <Show when={attrRows().length > 0}>
              <div class="mt-4">
                <h5 class="mb-2 text-xs font-semibold">
                  {t("workCatalog.attributesSection", {
                    count: attrRows().length,
                  })}
                </h5>
                <dl class="max-h-52 overflow-auto grid grid-cols-2 gap-x-3 gap-y-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 text-[11px]">
                  <For each={attrRows()}>
                    {(attr) => (
                      <div class="min-w-0">
                        <dt class="text-[var(--text-muted)] truncate">
                          {attr.name}
                        </dt>
                        <dd class="break-words">{attr.value}</dd>
                      </div>
                    )}
                  </For>
                </dl>
              </div>
            </Show>

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
