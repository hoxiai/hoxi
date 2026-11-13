import { getGatewayToken, requestJSON } from "./http";

export interface CatalogScope {
  storeId: string;
  storeName: string;
  platform: string;
}
export interface CatalogPublishContext {
  sessionID: string;
  directory: string;
}
export interface CatalogCapabilities {
  supported: boolean;
  read: boolean;
  sync: boolean;
  editDraft: boolean;
  newDraft: boolean;
  publishUpdate: boolean;
  publishNew: boolean;
  editableFields: string[];
}
export interface CatalogAuthorization {
  source: string;
  configured: boolean;
  verifiedRead: boolean;
  sellerLoginId: string;
  appKey: string;
  hasAppSecret: boolean;
  hasAccessToken: boolean;
  verifiedAt?: string;
  destination: string;
}
export interface CatalogCredentials {
  sellerLoginId: string;
  appKey: string;
  appSecret: string;
  accessToken: string;
}
export interface CatalogJob {
  id: string;
  status: string;
  fetched: number;
  total: number;
  message?: string;
  startedAt: string;
  finishedAt?: string;
}
export interface CatalogStatus {
  storeId: string;
  capabilities: CatalogCapabilities;
  authorization: CatalogAuthorization;
  productCount: number;
  lastSync?: {
    id: string;
    status: string;
    fetched: number;
    finishedAt?: string;
  };
  job?: CatalogJob;
}
export interface CatalogProduct {
  id: string;
  data: Record<string, unknown>;
  syncedAt: string;
}
export interface CatalogProductSummary {
  id: string;
  title: string;
  thumbnail?: string;
  status: string;
  category?: string;
  prices: unknown[];
  currency: string;
  stock: unknown;
  skuCount: number;
  hasDraft: boolean;
  syncedAt: string;
}
export interface CatalogPage {
  items: CatalogProductSummary[];
  total: number;
  nextOffset?: number;
  snapshot: string;
  statuses: string[];
}
export interface CatalogProductQuery {
  query?: string;
  status?: string;
  hasDraft?: boolean;
  offset?: number;
  limit?: number;
  snapshot?: string;
}
export interface CatalogPatch {
  subject?: string;
  description?: string;
  images?: string[];
}
export interface CatalogNewProduct {
  subject: string;
  description: string;
  images: string[];
  categoryId: string;
  sourceUrl?: string;
}
export interface CatalogReceipt {
  draftId: string;
  productId: string;
  status: string;
  message?: string;
  checkedAt: string;
}
export interface CatalogDraftSummary {
  id: string;
  kind: "create" | "update";
  productId: string;
  title: string;
  revision: string;
  status: string;
  createdAt: string;
  receipt?: CatalogReceipt;
}
export interface CatalogDraft {
  id: string;
  kind?: "create" | "update";
  productId: string;
  revision: string;
  base: CatalogProduct;
  patch: CatalogPatch;
  changes: { field: string; before: unknown; after: unknown }[];
  status: string;
  createdAt: string;
  receipt?: CatalogReceipt;
  newProduct?: CatalogNewProduct;
}

function endpoint(
  storeId: string,
  action: string,
  query?: Record<string, string | number | boolean | undefined>,
): string {
  const params = new URLSearchParams({ storeId });
  for (const [key, value] of Object.entries(query || {})) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  return `/api/hoxi/work/catalog/${action}?${params}`;
}

function call<T>(
  storeId: string,
  action: string,
  method = "GET",
  body?: unknown,
  query?: Record<string, string | number | boolean | undefined>,
): Promise<T> {
  return requestJSON<T>(endpoint(storeId, action, query), {
    method,
    headers: {
      Authorization: `Bearer ${getGatewayToken()}`,
      "X-Hoxi-Work-UI": "true",
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
}

export const workCatalogAPI = {
  status: (storeId: string) => call<CatalogStatus>(storeId, "status"),
  products: (storeId: string, query: CatalogProductQuery) =>
    call<CatalogPage>(storeId, "products", "GET", undefined, { ...query }),
  product: (storeId: string, productId: string) =>
    call<CatalogProduct>(storeId, "product", "GET", undefined, { productId }),
  drafts: (storeId: string) => call<CatalogDraftSummary[]>(storeId, "drafts"),
  draft: (storeId: string, draftId: string) =>
    call<CatalogDraft>(storeId, "draft", "GET", undefined, { draftId }),
  createDraft: (storeId: string, productId: string, changes: CatalogPatch) =>
    call<CatalogDraft>(storeId, "drafts", "POST", {
      kind: "update",
      productId,
      changes,
    }),
  intakeDraft: (storeId: string, newProduct: CatalogNewProduct) =>
    call<CatalogDraft>(storeId, "drafts", "POST", {
      kind: "create",
      newProduct,
    }),
  discardDraft: (storeId: string, draftId: string, revision: string) =>
    call<CatalogDraft>(storeId, "draft", "DELETE", { draftId, revision }),
  configure: (storeId: string, credentials: CatalogCredentials) =>
    call<CatalogAuthorization>(storeId, "authorization", "PUT", credentials),
  check: (storeId: string) =>
    call<CatalogAuthorization>(storeId, "authorization-check", "POST", {}),
  sync: (storeId: string, restart: boolean) =>
    call<CatalogJob>(storeId, "sync", "POST", { restart }),
  stop: (storeId: string) => call<{ ok: boolean }>(storeId, "sync", "DELETE"),
  publish: (
    storeId: string,
    draftId: string,
    revision: string,
    context: CatalogPublishContext,
  ) =>
    call<CatalogReceipt>(
      storeId,
      "publish",
      "POST",
      { draftId, revision },
      { ...context },
    ),
  verify: (storeId: string, draftId: string) =>
    call<CatalogReceipt>(storeId, "verify", "POST", { draftId }),
};

export async function readCatalogImport(file: File): Promise<unknown> {
  if (file.size > 2 * 1024 * 1024) throw new Error("catalog_import_too_large");
  return JSON.parse(await file.text()) as unknown;
}
