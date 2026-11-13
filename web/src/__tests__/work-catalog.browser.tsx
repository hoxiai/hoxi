import { createSignal } from "solid-js";
import { render } from "solid-js/web";
import WorkCatalogView from "../components/workcatalog/WorkCatalogView";
import { t } from "../i18n";
import { setGatewayToken } from "../services/http";
import type {
  CatalogDraft,
  CatalogDraftSummary,
  CatalogJob,
  CatalogProduct,
  CatalogStatus,
} from "../services/work-catalog";
import { activateWorkCatalogScope } from "../store/work-catalog";
import { setWorkspace } from "../store/workspace-state";
import type { CatalogPublishContext } from "../services/work-catalog";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const delay = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));
async function until(check: () => boolean): Promise<void> {
  for (let i = 0; i < 150; i++) {
    if (check()) return;
    await delay(20);
  }
  throw new Error("catalog UI did not reach the expected state");
}

export async function runBrowserTest(): Promise<string[]> {
  const previousFetch = window.fetch;
  const [storeId, setStoreId] = createSignal("store-a");
  const [publishContext, setPublishContext] =
    createSignal<CatalogPublishContext | null>(null);
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;inset:0;background:var(--bg-panel)";
  document.body.append(host);
  let configured = false,
    verified = false,
    synced = false;
  let job: CatalogJob | undefined;
  const drafts: CatalogDraft[] = [];
  let onlineWrites = 0;
  const product: CatalogProduct = {
    id: "123",
    data: {
      subject: "Original kettle",
      status: "published",
      description: "<img src=x onerror='window.catalogInjected=true'>",
      image: { images: [] },
      skuInfos: [{ skuId: "9223372036854775806" }],
    },
    syncedAt: "2026-11-13T00:00:00Z",
  };
  function status(id: string): CatalogStatus {
    if (job?.status === "running") {
      synced = true;
      job = { ...job, status: "completed", fetched: 1, total: 1 };
    }
    const isA = id === "store-a";
    return {
      storeId: id,
      productCount: isA && synced ? 1 : 0,
      authorization: {
        source: isA && configured ? "local" : "none",
        configured: isA && configured,
        verifiedRead: isA && verified,
        sellerLoginId: isA && configured ? "factory-a" : "",
        appKey: isA && configured ? "123456" : "",
        hasAppSecret: isA && configured,
        hasAccessToken: isA && configured,
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
      job: isA ? job : undefined,
    };
  }
  window.fetch = async (input, init) => {
    const url = new URL(String(input), location.origin);
    assertTrue(
      url.pathname.startsWith("/api/hoxi/work/catalog/"),
      "catalog browsing must not create or prompt a chat session",
    );
    const id = url.searchParams.get("storeId") || "";
    const action = url.pathname.split("/").at(-1);
    let body: unknown;
    switch (`${init?.method || "GET"} ${action}`) {
      case "GET status":
        body = status(id);
        break;
      case "PUT authorization":
        configured = true;
        body = status(id).authorization;
        break;
      case "POST authorization-check":
        verified = true;
        body = status(id).authorization;
        break;
      case "POST sync":
        job = {
          id: "job-1",
          status: "running",
          fetched: 0,
          total: 1,
          startedAt: "2026-11-13T00:00:00Z",
        };
        body = job;
        break;
      case "GET products":
        body = {
          items:
            id === "store-a" && synced
              ? [
                  {
                    id: "123",
                    title: "Original kettle",
                    status: "published",
                    stock: null,
                    skuCount: 1,
                    currency: "CNY",
                    prices: [{ price: 18.5 }],
                    hasDraft: drafts.length > 0,
                    syncedAt: product.syncedAt,
                  },
                ]
              : [],
          total: id === "store-a" && synced ? 1 : 0,
          statuses: ["published"],
          snapshot: "snapshot-1",
        };
        break;
      case "GET product":
        body = product;
        break;
      case "GET drafts":
        body =
          id === "store-a"
            ? drafts.map(
                (draft): CatalogDraftSummary => ({
                  id: draft.id,
                  kind: draft.kind || "update",
                  productId: draft.productId,
                  revision: draft.revision,
                  title: draft.newProduct?.subject || "Original kettle",
                  status: draft.status,
                  createdAt: draft.createdAt,
                }),
              )
            : [];
        break;
      case "POST drafts": {
        const data = JSON.parse(String(init?.body));
        if (data.kind === "update")
          assertEqual(
            Object.keys(data.changes).join(","),
            "subject",
            "editing a title must not submit stock, SKU or untouched content",
          );
        const draft: CatalogDraft = {
          id: `drf_${drafts.length + 1}`,
          kind: data.kind,
          productId: data.productId || "",
          revision: "immutable-revision",
          base:
            data.kind === "update"
              ? product
              : { id: "", data: {}, syncedAt: product.syncedAt },
          patch: data.changes || {},
          changes:
            data.kind === "update"
              ? [
                  {
                    field: "subject",
                    before: "Original kettle",
                    after: data.changes.subject,
                  },
                ]
              : [],
          newProduct: data.newProduct,
          status: "draft",
          createdAt: product.syncedAt,
        };
        drafts.push(draft);
        body = draft;
        break;
      }
      case "GET draft":
        body = drafts.find(
          (draft) => draft.id === url.searchParams.get("draftId"),
        );
        break;
      case "POST publish":
        onlineWrites++;
        throw new Error("unexpected online write");
      default:
        throw new Error(
          `Unexpected catalog request: ${init?.method || "GET"} ${action}`,
        );
    }
    return new Response(JSON.stringify(body), {
      headers: { "Content-Type": "application/json" },
    });
  };
  setGatewayToken("catalog-browser-fixture");
  const dispose = render(
    () => (
      <WorkCatalogView
        storeId={storeId()}
        storeName={storeId()}
        platform="1688"
        publishContext={publishContext()}
      />
    ),
    host,
  );
  const button = (label: string): HTMLButtonElement => {
    const match = Array.from(host.querySelectorAll("button")).find(
      (item) => item.textContent?.trim() === label,
    );
    if (!match) throw new Error(`Missing catalog button: ${label}`);
    return match;
  };
  const results: string[] = [];
  try {
    await until(
      () => host.textContent?.includes(t("workCatalog.unconfigured")) || false,
    );
    assertTrue(
      button(t("workCatalog.sync")).disabled,
      "unconfigured store cannot sync",
    );
    button(t("workCatalog.authorize")).click();
    await until(
      () => !!host.querySelector("[data-testid=catalog-authorization]"),
    );
    const auth = host.querySelector("[data-testid=catalog-authorization]")!;
    const inputs = auth.querySelectorAll<HTMLInputElement>("input");
    [
      "factory-a",
      "123456",
      "browser-secret-never-persist",
      "browser-token-never-persist",
    ].forEach((value, index) => {
      inputs[index].value = value;
      inputs[index].dispatchEvent(new Event("input", { bubbles: true }));
    });
    auth
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await until(
      () => configured && inputs[2].value === "" && inputs[3].value === "",
    );
    button(t("workCatalog.checkAuthorization")).click();
    await until(
      () => host.textContent?.includes(t("workCatalog.verified")) || false,
    );
    assertTrue(
      !Object.values(localStorage).some((value) =>
        String(value).includes("browser-secret-never-persist"),
      ),
      "platform secrets must not enter localStorage",
    );
    results.push(
      "visual authorization clears secret inputs and distinguishes configured credentials from verified read access",
    );
    auth.querySelector<HTMLButtonElement>("button")!.click();
    button(t("workCatalog.sync")).click();
    await until(() => host.textContent?.includes("Original kettle") || false);
    assertTrue(
      host.textContent?.includes(t("workCatalog.unknown")) || false,
      "missing stock must remain unknown",
    );
    button(t("workCatalog.view")).click();
    await until(() => !!host.querySelector("[data-testid=catalog-detail]"));
    await until(() => !!host.querySelector("pre"));
    assertTrue(
      !host.querySelector("img[src=x]"),
      "platform HTML must render as text, not execute",
    );
    button(t("workCatalog.edit")).click();
    const editor = host.querySelector("[data-testid=catalog-draft-editor]")!;
    const title = editor.querySelector<HTMLInputElement>("input")!;
    title.value = "Improved kettle";
    title.dispatchEvent(new Event("input", { bubbles: true }));
    editor
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await until(() => host.textContent?.includes("Improved kettle") || false);
    assertEqual(drafts[0].patch.subject, "Improved kettle");
    assertEqual(onlineWrites, 0);
    results.push(
      "sync, list browsing and field-level edit drafts work without invoking a model or changing the online product",
    );
    button(t("workCatalog.newProduct")).click();
    const intake = host.querySelector("[data-testid=catalog-draft-editor]")!;
    const newTitle = intake.querySelector<HTMLInputElement>(
      "input:not([type=file])",
    )!;
    newTitle.value = "New factory item";
    newTitle.dispatchEvent(new Event("input", { bubbles: true }));
    intake
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await until(
      () =>
        host.textContent?.includes(t("workCatalog.createUnsupported")) || false,
    );
    assertEqual(drafts[1].kind, "create");
    assertTrue(
      !Array.from(host.querySelectorAll("button")).some(
        (item) => item.textContent?.trim() === t("workCatalog.publish"),
      ),
      "unsupported platform creation must not offer an online publish action",
    );
    results.push(
      "new-product intake stays an explicit local draft until the adapter supports a real creation API",
    );
    setPublishContext({ sessionID: "ses_catalog", directory: "/catalog" });
    const permission = {
      id: "per_rnr_catalog",
      sessionID: "ses_catalog",
      permission: "hoxi_work_publish",
      patterns: ["Publish store product"],
      always: [],
      metadata: {
        tool: "hoxi_work_publish",
        destructive: true,
        storeId: "store-a",
        args: JSON.stringify({
          draftId: "drf_1",
          revision: "immutable-revision",
        }),
      },
    };
    setWorkspace("pendingPermissions", [
      permission,
      {
        ...permission,
        id: "per_rnr_foreign",
        metadata: {
          ...permission.metadata,
          storeId: "store-b",
          args: "foreign-store-approval",
        },
      },
    ]);
    await until(
      () => !!host.querySelector("[data-testid=catalog-publish-approval]"),
    );
    const approval = host.querySelector(
      "[data-testid=catalog-publish-approval]",
    )!;
    assertTrue(
      approval.textContent?.includes(t("approval.allowOnce")) || false,
      "publication approval must be actionable without returning to chat",
    );
    assertTrue(
      !approval.textContent?.includes("foreign-store-approval"),
      "another store's publication approval leaked into the catalog",
    );
    results.push(
      "the shared desktop approval card remains visible and store-scoped while the chat view is hidden",
    );
    setWorkspace("pendingPermissions", []);
    setStoreId("store-b");
    await until(
      () =>
        !!host.textContent?.includes("store-b") &&
        !host.querySelector("[data-testid=catalog-detail]"),
    );
    await until(
      () => host.textContent?.includes(t("workCatalog.unconfigured")) || false,
    );
    assertTrue(
      !host.textContent?.includes("Original kettle"),
      "switching stores leaked the previous catalog",
    );
    assertEqual(onlineWrites, 0);
    results.push(
      "switching stores clears product, draft and authorization state while preserving read-only browsing without chat",
    );
  } finally {
    dispose();
    host.remove();
    activateWorkCatalogScope(null);
    setWorkspace("pendingPermissions", []);
    window.fetch = previousFetch;
    setGatewayToken("");
  }
  return results;
}
