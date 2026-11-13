import { getGatewayToken, setGatewayToken } from "../services/http";
import {
  workCatalogAPI,
  type CatalogDraft,
  type CatalogPage,
  type CatalogProduct,
  type CatalogStatus,
} from "../services/work-catalog";
import {
  activateWorkCatalogScope,
  catalogDisplayImages,
  closeCatalogDetail,
  executeCatalogDraftAction,
  loadCatalogProducts,
  openCatalogEditor,
  refreshCatalogStatus,
  saveCatalogDraft,
  selectCatalogProduct,
  setWorkCatalogState,
  startCatalogSync,
  updateCatalogEditor,
  workCatalogState,
} from "../store/work-catalog";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function page(id: string): CatalogPage {
  return {
    items: [
      {
        id,
        title: id,
        status: "published",
        currency: "CNY",
        prices: [],
        stock: null,
        skuCount: 1,
        hasDraft: false,
        syncedAt: "2026-11-13T00:00:00Z",
      },
    ],
    total: 1,
    snapshot: id,
    statuses: ["published"],
  };
}
function status(id: string): CatalogStatus {
  return {
    storeId: id,
    productCount: 1,
    authorization: {
      source: "local",
      configured: true,
      verifiedRead: false,
      sellerLoginId: "factory-a",
      appKey: "123",
      hasAppSecret: true,
      hasAccessToken: true,
      destination: "https://gw.open.1688.com",
    },
    capabilities: {
      supported: true,
      read: true,
      sync: true,
      editDraft: true,
      newDraft: true,
      publishUpdate: true,
      publishNew: false,
      editableFields: ["subject", "description", "images"],
    },
  };
}
const product: CatalogProduct = {
  id: "123",
  data: {
    subject: "Original",
    description: "Original description",
    image: { images: ["img/ibank/a.jpg"] },
  },
  syncedAt: "2026-11-13T00:00:00Z",
};
const draft: CatalogDraft = {
  id: "drf_test",
  kind: "update",
  productId: "123",
  revision: "revision-1",
  base: product,
  patch: { subject: "Improved" },
  changes: [{ field: "subject", before: "Original", after: "Improved" }],
  status: "draft",
  createdAt: "2026-11-13T00:00:00Z",
};

async function fixture(run: () => Promise<void>): Promise<void> {
  const original = { ...workCatalogAPI };
  activateWorkCatalogScope({
    storeId: "test-reset",
    storeName: "reset",
    platform: "1688",
  });
  activateWorkCatalogScope({
    storeId: "store-a",
    storeName: "A",
    platform: "1688",
  });
  setWorkCatalogState("status", status("store-a"));
  workCatalogAPI.products = async () => page("fresh");
  workCatalogAPI.drafts = async () => [];
  try {
    await run();
  } finally {
    Object.assign(workCatalogAPI, original);
    activateWorkCatalogScope(null);
  }
}

describe("Work product catalog isolation", () => {
  it("discards stale store and pagination replies without creating a chat session", async () =>
    fixture(async () => {
      const old = deferred<CatalogPage>();
      workCatalogAPI.products = () => old.promise;
      const pending = loadCatalogProducts();
      activateWorkCatalogScope({
        storeId: "store-b",
        storeName: "B",
        platform: "1688",
      });
      old.resolve(page("store-a-product"));
      await pending;
      assertEqual(workCatalogState.page, null);
      const first = deferred<CatalogPage>(),
        second = deferred<CatalogPage>();
      let calls = 0;
      workCatalogAPI.products = () =>
        ++calls === 1 ? first.promise : second.promise;
      const early = loadCatalogProducts(),
        late = loadCatalogProducts();
      second.resolve(page("newest"));
      await late;
      first.resolve(page("oldest"));
      await early;
      assertEqual(workCatalogState.page?.items[0]?.id, "newest");
    }));

  it("does not let an old status response erase a newly started sync job", async () =>
    fixture(async () => {
      const old = deferred<CatalogStatus>();
      workCatalogAPI.status = () => old.promise;
      const pending = refreshCatalogStatus();
      workCatalogAPI.sync = async () => ({
        id: "job-new",
        status: "running",
        fetched: 0,
        total: 0,
        startedAt: "2026-11-13T00:00:00Z",
      });
      await startCatalogSync();
      old.resolve(status("store-a"));
      await pending;
      assertEqual(workCatalogState.status?.job?.id, "job-new");
    }));

  it("does not reopen a closed detail panel when an immutable draft save finishes", async () =>
    fixture(async () => {
      workCatalogAPI.product = async () => product;
      await selectCatalogProduct(product.id);
      openCatalogEditor("update");
      updateCatalogEditor("subject", "Improved");
      const saved = deferred<CatalogDraft>();
      workCatalogAPI.createDraft = async (_store, id, changes) => {
        assertEqual(id, "123");
        assertEqual(changes.subject, "Improved");
        assertEqual(changes.description, undefined);
        assertEqual(changes.images, undefined);
        return saved.promise;
      };
      const pending = saveCatalogDraft();
      closeCatalogDetail();
      saved.resolve(draft);
      await pending;
      assertEqual(workCatalogState.draft, null);
      assertEqual(workCatalogState.editor, null);
    }));

  it("requires an explicit store-bound publication task before issuing an online write", async () =>
    fixture(async () => {
      setWorkCatalogState("draft", draft);
      let writes = 0;
      workCatalogAPI.publish = async () => {
        writes++;
        throw new Error("unexpected write");
      };
      await executeCatalogDraftAction("publish");
      assertEqual(writes, 0);
      assertTrue(workCatalogState.error.length > 0);
    }));

  it("encodes store scope and keeps authorization values out of URLs", async () => {
    const originalFetch = globalThis.fetch;
    const previousToken = getGatewayToken();
    setGatewayToken("local-gateway-test");
    try {
      globalThis.fetch = async (input, init) => {
        const url = String(input);
        assertTrue(url.includes("storeId=store%2Fa"));
        assertTrue(
          !url.includes("private-token") && !url.includes("private-secret"),
        );
        const headers = init?.headers as Record<string, string>;
        assertEqual(headers.Authorization, "Bearer local-gateway-test");
        assertEqual(headers["X-Hoxi-Work-UI"], "true");
        assertTrue(String(init?.body).includes("private-token"));
        return new Response(JSON.stringify(status("store/a").authorization), {
          status: 200,
        });
      };
      await workCatalogAPI.configure("store/a", {
        sellerLoginId: "factory-a",
        appKey: "123",
        appSecret: "private-secret",
        accessToken: "private-token",
      });
    } finally {
      globalThis.fetch = originalFetch;
      setGatewayToken(previousToken);
    }
  });

  it("renders only trusted image-bank URLs from untrusted platform fields", () => {
    const images = catalogDisplayImages({
      ...product,
      data: {
        image: {
          images: [
            "img/ibank/a.jpg",
            "javascript:alert(1)",
            "https://evil.example/a.jpg",
          ],
        },
      },
    });
    assertEqual(images.length, 1);
    assertEqual(images[0], "https://cbu01.alicdn.com/img/ibank/a.jpg");
  });
});
