import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { locale, t } from "../i18n";
import { errorMessageOf } from "../services/http";
import { setWorkspace, workspace } from "./workspace-state";
import {
  readCatalogImport,
  workCatalogAPI,
  type CatalogBrowserSyncInput,
  type CatalogBrowserSyncResult,
  type CatalogCredentials,
  type CatalogDraft,
  type CatalogDraftSummary,
  type CatalogNewProduct,
  type CatalogPage,
  type CatalogPatch,
  type CatalogProduct,
  type CatalogPublishContext,
  type CatalogScope,
  type CatalogStatus,
} from "../services/work-catalog";

export type CatalogTab = "products" | "drafts" | "history" | "sources";
interface Editor {
  mode: "create" | "update";
  subject: string;
  description: string;
  images: string;
  categoryId: string;
  sourceUrl: string;
}
interface CatalogState {
  scope: CatalogScope | null;
  status: CatalogStatus | null;
  page: CatalogPage | null;
  drafts: CatalogDraftSummary[];
  tab: CatalogTab;
  query: string;
  statusFilter: string;
  draftOnly: boolean;
  offset: number;
  product: CatalogProduct | null;
  draft: CatalogDraft | null;
  editor: Editor | null;
  loading: boolean;
  detailLoading: boolean;
  saving: boolean;
  publishing: boolean;
  error: string;
  authorizationOpen: boolean;
  publishContext: CatalogPublishContext | null;
}
function initialState(): CatalogState {
  return {
    scope: null,
    status: null,
    page: null,
    drafts: [],
    tab: "products",
    query: "",
    statusFilter: "",
    draftOnly: false,
    offset: 0,
    product: null,
    draft: null,
    editor: null,
    loading: false,
    detailLoading: false,
    saving: false,
    publishing: false,
    error: "",
    authorizationOpen: false,
    publishContext: null,
  };
}
export const [isWorkCatalogViewOpen, setWorkCatalogViewOpen] =
  createSignal(false);
export const [workCatalogState, setWorkCatalogState] =
  createStore<CatalogState>(initialState());
let scopeGeneration = 0;
let queryGeneration = 0;
let detailGeneration = 0;
let statusGeneration = 0;
let draftsGeneration = 0;
let filterTimer: ReturnType<typeof setTimeout> | undefined;

export function openWorkCatalogView(): void {
  setWorkspace("inspectorOpen", false);
  setWorkCatalogViewOpen(true);
}
export function closeWorkCatalogView(): void {
  setWorkCatalogViewOpen(false);
}

export function activateWorkCatalogScope(scope: CatalogScope | null): void {
  if (
    scope?.storeId === workCatalogState.scope?.storeId &&
    scope?.platform === workCatalogState.scope?.platform
  ) {
    if (scope) setWorkCatalogState("scope", { ...scope });
    return;
  }
  scopeGeneration++;
  queryGeneration++;
  detailGeneration++;
  statusGeneration++;
  draftsGeneration++;
  if (filterTimer) clearTimeout(filterTimer);
  setWorkCatalogState(initialState());
  setWorkCatalogState("scope", scope ? { ...scope } : null);
}

export function setCatalogPublishContext(
  context: CatalogPublishContext | null,
): void {
  setWorkCatalogState("publishContext", context ? { ...context } : null);
}
export function catalogPendingApprovals() {
  const scope = workCatalogState.scope;
  const context = workCatalogState.publishContext;
  if (!scope || !context) return [];
  return workspace.pendingPermissions.filter(
    (request) =>
      request.sessionID === context.sessionID &&
      request.permission === "hoxi_work_publish" &&
      request.metadata?.storeId === scope.storeId,
  );
}

export function setCatalogTab(tab: CatalogTab): void {
  setWorkCatalogState("tab", tab);
}
export function setCatalogAuthorizationOpen(open: boolean): void {
  setWorkCatalogState("authorizationOpen", open);
}
export function closeCatalogDetail(): void {
  detailGeneration++;
  setWorkCatalogState({
    product: null,
    draft: null,
    editor: null,
    detailLoading: false,
  });
}
function report(error: unknown): void {
  setWorkCatalogState("error", errorMessageOf(error, t("workCatalog.failed")));
}

export async function loadCatalogProducts(offset = 0): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration;
  const request = ++queryGeneration;
  setWorkCatalogState({ loading: true, error: "" });
  try {
    const page = await workCatalogAPI.products(scope.storeId, {
      query: workCatalogState.query,
      status: workCatalogState.statusFilter,
      hasDraft: workCatalogState.draftOnly,
      offset,
      limit: 20,
      snapshot: offset > 0 ? workCatalogState.page?.snapshot : undefined,
    });
    if (generation !== scopeGeneration || request !== queryGeneration) return;
    setWorkCatalogState({ page, offset });
  } catch (error) {
    if (generation !== scopeGeneration || request !== queryGeneration) return;
    if (
      offset > 0 &&
      errorMessageOf(error, "").includes("catalog_snapshot_changed")
    ) {
      await loadCatalogProducts(0);
    } else report(error);
  } finally {
    if (generation === scopeGeneration && request === queryGeneration)
      setWorkCatalogState("loading", false);
  }
}

export async function refreshCatalogStatus(): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration;
  const request = ++statusGeneration;
  try {
    const status = await workCatalogAPI.status(scope.storeId);
    if (generation !== scopeGeneration || request !== statusGeneration) return;
    const completed =
      workCatalogState.status?.job?.status === "running" &&
      status.job?.status !== "running";
    setWorkCatalogState("status", status);
    if (completed)
      await Promise.all([loadCatalogProducts(), loadCatalogDrafts()]);
  } catch (error) {
    if (generation === scopeGeneration && request === statusGeneration)
      report(error);
  }
}

export async function loadCatalogDrafts(): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration;
  const request = ++draftsGeneration;
  try {
    const drafts = await workCatalogAPI.drafts(scope.storeId);
    if (generation === scopeGeneration && request === draftsGeneration)
      setWorkCatalogState("drafts", drafts);
  } catch (error) {
    if (generation === scopeGeneration && request === draftsGeneration)
      report(error);
  }
}

export function observeWorkCatalog(): () => void {
  let disposed = false;
  let polling = false;
  void refreshCatalogStatus().then(async () => {
    if (!disposed && workCatalogState.status?.capabilities.supported)
      await Promise.all([loadCatalogProducts(), loadCatalogDrafts()]);
  });
  const timer = setInterval(() => {
    if (
      disposed ||
      polling ||
      workCatalogState.status?.job?.status !== "running"
    )
      return;
    polling = true;
    void refreshCatalogStatus().finally(() => {
      polling = false;
    });
  }, 1500);
  return () => {
    disposed = true;
    clearInterval(timer);
    if (filterTimer) clearTimeout(filterTimer);
  };
}

export function updateCatalogFilters(
  query: string,
  status: string,
  draftOnly: boolean,
): void {
  setWorkCatalogState({ query, statusFilter: status, draftOnly });
  if (filterTimer) clearTimeout(filterTimer);
  filterTimer = setTimeout(() => {
    void loadCatalogProducts();
  }, 200);
}

export async function selectCatalogProduct(id: string): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration,
    request = ++detailGeneration;
  setWorkCatalogState({
    product: null,
    draft: null,
    editor: null,
    detailLoading: true,
    error: "",
  });
  try {
    const product = await workCatalogAPI.product(scope.storeId, id);
    if (generation === scopeGeneration && request === detailGeneration)
      setWorkCatalogState("product", product);
  } catch (error) {
    if (generation === scopeGeneration && request === detailGeneration)
      report(error);
  } finally {
    if (generation === scopeGeneration && request === detailGeneration)
      setWorkCatalogState("detailLoading", false);
  }
}

export async function selectCatalogDraft(id: string): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration,
    request = ++detailGeneration;
  setWorkCatalogState({
    product: null,
    draft: null,
    editor: null,
    detailLoading: true,
    error: "",
  });
  try {
    const draft = await workCatalogAPI.draft(scope.storeId, id);
    if (generation === scopeGeneration && request === detailGeneration)
      setWorkCatalogState("draft", draft);
  } catch (error) {
    if (generation === scopeGeneration && request === detailGeneration)
      report(error);
  } finally {
    if (generation === scopeGeneration && request === detailGeneration)
      setWorkCatalogState("detailLoading", false);
  }
}

export function catalogImageURLs(product: CatalogProduct | null): string[] {
  const image = product?.data.image;
  if (
    !image ||
    typeof image !== "object" ||
    !Array.isArray((image as Record<string, unknown>).images)
  )
    return [];
  return ((image as Record<string, unknown>).images as unknown[]).filter(
    (item): item is string => typeof item === "string",
  );
}
export function catalogProductText(
  product: CatalogProduct | null,
  field: string,
): string {
  const value = product?.data[field];
  return typeof value === "string" ? value : "";
}
export function catalogDisplayImages(product: CatalogProduct | null): string[] {
  return catalogImageURLs(product).flatMap((raw) => {
    const candidate = raw.startsWith("img/ibank/")
      ? `https://cbu01.alicdn.com/${raw}`
      : raw;
    try {
      const url = new URL(candidate);
      return url.protocol === "https:" &&
        !url.username &&
        !url.password &&
        !url.port &&
        url.hostname.endsWith(".alicdn.com") &&
        url.pathname.startsWith("/img/ibank/")
        ? [url.href]
        : [];
    } catch {
      return [];
    }
  });
}

export function catalogPriceLabel(prices: unknown[], currency = "CNY"): string {
  const values = prices.flatMap((price) => {
    if (!price || typeof price !== "object") return [];
    const raw = (price as Record<string, unknown>).price;
    if (
      (typeof raw !== "number" && typeof raw !== "string") ||
      (typeof raw === "string" && !raw.trim())
    )
      return [];
    const value = Number(raw);
    return Number.isFinite(value) && value >= 0 ? [value] : [];
  });
  if (!values.length) return t("workCatalog.unknown");
  const min = Math.min(...values),
    max = Math.max(...values);
  try {
    const format = new Intl.NumberFormat(locale(), {
      style: "currency",
      currency,
    });
    return min === max
      ? format.format(min)
      : `${format.format(min)}–${format.format(max)}`;
  } catch {
    return `${min}–${max} ${currency}`;
  }
}

export function openCatalogEditor(mode: "create" | "update"): void {
  detailGeneration++;
  const product = mode === "update" ? workCatalogState.product : null;
  if (mode === "update" && !product) return;
  setWorkCatalogState({
    draft: null,
    editor: {
      mode,
      subject: catalogProductText(product, "subject"),
      description: catalogProductText(product, "description"),
      images: catalogImageURLs(product).join("\n"),
      categoryId: "",
      sourceUrl: "",
    },
    error: "",
  });
  if (mode === "create") setWorkCatalogState("product", null);
}
export function updateCatalogEditor(
  field: "subject" | "description" | "images" | "categoryId" | "sourceUrl",
  value: string,
): void {
  detailGeneration++;
  if (workCatalogState.editor) setWorkCatalogState("editor", field, value);
}

export function openCatalogNewDraft(input: CatalogNewProduct): void {
  openCatalogEditor("create");
  setWorkCatalogState("editor", {
    mode: "create",
    subject: input.subject,
    description: input.description,
    images: input.images.join("\n"),
    categoryId: input.categoryId,
    sourceUrl: input.sourceUrl || "",
  });
}

export async function importCatalogDraft(file: File): Promise<void> {
  const generation = scopeGeneration;
  const request = ++detailGeneration;
  try {
    const value = await readCatalogImport(file);
    if (generation !== scopeGeneration || request !== detailGeneration) return;
    if (!value || typeof value !== "object" || Array.isArray(value))
      throw new Error(t("workCatalog.invalidImport"));
    const data = value as Record<string, unknown>;
    const source =
      data.source1688 && typeof data.source1688 === "object"
        ? (data.source1688 as Record<string, unknown>)
        : data;
    const title = data.subject ?? data.titleCn ?? data.title ?? source.title;
    const images = data.images ?? source.images ?? [];
    const description = data.description ?? "";
    if (
      typeof title !== "string" ||
      !Array.isArray(images) ||
      images.some((item) => typeof item !== "string") ||
      typeof description !== "string"
    )
      throw new Error(t("workCatalog.invalidImport"));
    openCatalogEditor("create");
    setWorkCatalogState("editor", {
      mode: "create",
      subject: title,
      description,
      images: images.join("\n"),
      categoryId: typeof data.categoryId === "string" ? data.categoryId : "",
      sourceUrl: typeof source.url === "string" ? source.url : "",
    });
  } catch (error) {
    if (generation === scopeGeneration) report(error);
  }
}

export async function saveCatalogDraft(): Promise<void> {
  const scope = workCatalogState.scope,
    editor = workCatalogState.editor;
  if (!scope || !editor || workCatalogState.saving) return;
  const generation = scopeGeneration;
  const detailRequest = detailGeneration;
  const subject = editor.subject,
    description = editor.description;
  const images = editor.images
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  setWorkCatalogState({ saving: true, error: "" });
  try {
    let draft: CatalogDraft;
    if (editor.mode === "create") {
      const input: CatalogNewProduct = {
        subject,
        description,
        images,
        categoryId: editor.categoryId,
        sourceUrl: editor.sourceUrl,
      };
      draft = await workCatalogAPI.intakeDraft(scope.storeId, input);
    } else {
      const product = workCatalogState.product;
      if (!product) return;
      const changes: CatalogPatch = {};
      if (subject !== catalogProductText(product, "subject"))
        changes.subject = subject;
      if (description !== catalogProductText(product, "description"))
        changes.description = description;
      if (JSON.stringify(images) !== JSON.stringify(catalogImageURLs(product)))
        changes.images = images;
      draft = await workCatalogAPI.createDraft(
        scope.storeId,
        product.id,
        changes,
      );
    }
    if (generation !== scopeGeneration) return;
    if (detailRequest === detailGeneration)
      setWorkCatalogState({
        draft,
        editor: null,
        product: null,
        tab: "drafts",
      });
    await Promise.all([loadCatalogDrafts(), loadCatalogProducts()]);
  } catch (error) {
    if (generation === scopeGeneration) report(error);
  } finally {
    if (generation === scopeGeneration) setWorkCatalogState("saving", false);
  }
}

export async function configureCatalogAuthorization(
  credentials: CatalogCredentials,
): Promise<boolean> {
  const scope = workCatalogState.scope;
  if (!scope || workCatalogState.saving) return false;
  const generation = scopeGeneration;
  setWorkCatalogState({ saving: true, error: "" });
  try {
    await workCatalogAPI.configure(scope.storeId, credentials);
    if (generation !== scopeGeneration) return false;
    await refreshCatalogStatus();
    return true;
  } catch (error) {
    if (generation === scopeGeneration) report(error);
    return false;
  } finally {
    if (generation === scopeGeneration) setWorkCatalogState("saving", false);
  }
}

export async function configureCatalogBrowserAuthorization(
  sellerLoginId: string,
  clearCredentials = false,
): Promise<boolean> {
  const scope = workCatalogState.scope;
  const cleanSeller = sellerLoginId.trim();
  if (!scope || !cleanSeller || workCatalogState.saving) return false;
  const generation = scopeGeneration;
  setWorkCatalogState({ saving: true, error: "" });
  try {
    await workCatalogAPI.configureBrowser(scope.storeId, {
      sellerLoginId: cleanSeller,
      clearCredentials,
    });
    if (generation !== scopeGeneration) return false;
    await refreshCatalogStatus();
    return true;
  } catch (error) {
    if (generation === scopeGeneration) report(error);
    return false;
  } finally {
    if (generation === scopeGeneration) setWorkCatalogState("saving", false);
  }
}

export async function syncCatalogBrowserProducts(
  input: CatalogBrowserSyncInput,
): Promise<CatalogBrowserSyncResult | null> {
  const scope = workCatalogState.scope;
  if (!scope) return null;
  const generation = scopeGeneration;
  setWorkCatalogState("error", "");
  try {
    const res = await workCatalogAPI.syncBrowser(scope.storeId, input);
    if (generation !== scopeGeneration) return null;
    await Promise.all([refreshCatalogStatus(), loadCatalogProducts(0)]);
    if (
      workCatalogState.product &&
      input.products.some((item) => item.id === workCatalogState.product?.id)
    ) {
      await selectCatalogProduct(workCatalogState.product.id);
    }
    return res;
  } catch (error) {
    if (generation === scopeGeneration) report(error);
    return null;
  }
}

export async function checkCatalogAuthorization(): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope || workCatalogState.saving) return;
  const generation = scopeGeneration;
  setWorkCatalogState({ saving: true, error: "" });
  try {
    const auth = await workCatalogAPI.check(scope.storeId);
    if (generation === scopeGeneration && workCatalogState.status)
      setWorkCatalogState("status", "authorization", auth);
  } catch (error) {
    if (generation === scopeGeneration) {
      await refreshCatalogStatus();
      if (generation === scopeGeneration) report(error);
    }
  } finally {
    if (generation === scopeGeneration) setWorkCatalogState("saving", false);
  }
}

export async function startCatalogSync(restart = false): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope || workCatalogState.status?.job?.status === "running") return;
  const generation = scopeGeneration;
  statusGeneration++;
  setWorkCatalogState("error", "");
  try {
    const job = await workCatalogAPI.sync(scope.storeId, restart);
    if (generation === scopeGeneration) statusGeneration++;
    if (generation === scopeGeneration && workCatalogState.status)
      setWorkCatalogState("status", "job", job);
  } catch (error) {
    if (generation === scopeGeneration) report(error);
  }
}
export async function stopCatalogSync(): Promise<void> {
  const scope = workCatalogState.scope;
  if (!scope) return;
  const generation = scopeGeneration;
  try {
    await workCatalogAPI.stop(scope.storeId);
    if (generation === scopeGeneration) await refreshCatalogStatus();
  } catch (error) {
    if (generation === scopeGeneration) report(error);
  }
}

export async function executeCatalogDraftAction(
  action: "publish" | "verify" | "discard",
): Promise<void> {
  const scope = workCatalogState.scope,
    draft = workCatalogState.draft;
  if (!scope || !draft || workCatalogState.publishing) return;
  const generation = scopeGeneration;
  setWorkCatalogState({ publishing: true, error: "" });
  try {
    if (action === "publish") {
      const context = workCatalogState.publishContext;
      if (!context) throw new Error(t("workCatalog.sessionRequired"));
      await workCatalogAPI.publish(
        scope.storeId,
        draft.id,
        draft.revision,
        context,
      );
    } else if (action === "verify")
      await workCatalogAPI.verify(scope.storeId, draft.id);
    else
      await workCatalogAPI.discardDraft(
        scope.storeId,
        draft.id,
        draft.revision,
      );
    if (generation !== scopeGeneration) return;
    if (workCatalogState.draft?.id === draft.id)
      await selectCatalogDraft(draft.id);
    await Promise.all([loadCatalogDrafts(), loadCatalogProducts()]);
  } catch (error) {
    if (generation === scopeGeneration) report(error);
  } finally {
    if (generation === scopeGeneration)
      setWorkCatalogState("publishing", false);
  }
}
