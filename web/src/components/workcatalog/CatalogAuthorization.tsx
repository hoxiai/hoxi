import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import type { CatalogBrowserSyncController } from "../../services/work-catalog";
import {
  checkCatalogAuthorization,
  configureCatalogAuthorization,
  configureCatalogBrowserAuthorization,
  setCatalogAuthorizationOpen,
  workCatalogState,
} from "../../store/work-catalog";

const inputClass =
  "w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-2 text-sm outline-none focus:border-[var(--accent-primary)]";
const secondaryBtnClass =
  "rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50";

export default function CatalogAuthorization(props: {
  browserController?: CatalogBrowserSyncController;
}) {
  const [seller, setSeller] = createSignal(
    workCatalogState.status?.authorization.sellerLoginId || "",
  );
  const [browserSeller, setBrowserSeller] = createSignal(
    workCatalogState.status?.authorization.sellerLoginId || "",
  );
  const [appKey, setAppKey] = createSignal(
    workCatalogState.status?.authorization.appKey || "",
  );
  const [secret, setSecret] = createSignal("");
  const [token, setToken] = createSignal("");

  createEffect(() => {
    const savedSeller =
      workCatalogState.status?.authorization.sellerLoginId || "";
    const detected =
      props.browserController?.browserState()?.detectedSellerId || "";
    if (!browserSeller().trim() && (savedSeller || detected)) {
      setBrowserSeller(savedSeller || detected);
    }
    if (!seller().trim() && savedSeller) {
      setSeller(savedSeller);
    }
  });

  onCleanup(() => {
    setSecret("");
    setToken("");
  });

  const save = async () => {
    if (
      await configureCatalogAuthorization({
        sellerLoginId: seller(),
        appKey: appKey(),
        appSecret: secret(),
        accessToken: token(),
      })
    ) {
      setSecret("");
      setToken("");
    }
  };

  const bindBrowser = async () => {
    const targetSeller = browserSeller().trim();
    if (!targetSeller) return;
    if (await configureCatalogBrowserAuthorization(targetSeller)) {
      setSeller(targetSeller);
    }
  };

  const detectSeller = async () => {
    const detected = await props.browserController?.detectSellerLoginId();
    if (detected) {
      setBrowserSeller(detected);
      if (!seller().trim()) setSeller(detected);
    }
  };

  const renderOpenApiForm = () => (
    <div class="space-y-3">
      <p class="text-xs text-[var(--text-muted)]">
        {t("workCatalog.destination", {
          url:
            workCatalogState.status?.authorization.destination ||
            "https://gw.open.1688.com",
        })}
      </p>
      <form
        autocomplete="off"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
        class="grid gap-3 sm:grid-cols-2"
      >
        <label class="space-y-1 text-xs">
          {t("workCatalog.seller")}
          <input
            required
            class={inputClass}
            value={seller()}
            onInput={(event) => setSeller(event.currentTarget.value)}
          />
        </label>
        <label class="space-y-1 text-xs">
          {t("workCatalog.appKey")}
          <input
            required
            class={inputClass}
            value={appKey()}
            inputmode="numeric"
            onInput={(event) => setAppKey(event.currentTarget.value)}
          />
        </label>
        <label class="space-y-1 text-xs">
          {t("workCatalog.appSecret")}
          <input
            type="password"
            autocomplete="new-password"
            class={inputClass}
            value={secret()}
            placeholder={
              workCatalogState.status?.authorization.hasAppSecret
                ? t("workCatalog.keepSecret")
                : ""
            }
            onInput={(event) => setSecret(event.currentTarget.value)}
          />
        </label>
        <label class="space-y-1 text-xs">
          {t("workCatalog.accessToken")}
          <input
            type="password"
            autocomplete="new-password"
            class={inputClass}
            value={token()}
            placeholder={
              workCatalogState.status?.authorization.hasAccessToken
                ? t("workCatalog.keepSecret")
                : ""
            }
            onInput={(event) => setToken(event.currentTarget.value)}
          />
        </label>
        <div class="sm:col-span-2 flex flex-wrap items-center gap-2">
          <button
            type="submit"
            disabled={workCatalogState.saving}
            class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
          >
            {workCatalogState.saving
              ? t("workCatalog.saving")
              : t("workCatalog.saveAuthorization")}
          </button>
          <button
            type="button"
            disabled={
              workCatalogState.saving ||
              !workCatalogState.status?.authorization.configured ||
              workCatalogState.status?.authorization.source === "browser"
            }
            onClick={() => void checkCatalogAuthorization()}
            class={secondaryBtnClass}
          >
            {t("workCatalog.checkAuthorization")}
          </button>
          <Show
            when={
              workCatalogState.status?.authorization.verifiedRead &&
              workCatalogState.status?.authorization.source !== "browser"
            }
          >
            <span class="text-xs text-emerald-600">
              {t("workCatalog.verified")}
            </span>
          </Show>
        </div>
      </form>
      <Show
        when={workCatalogState.status?.authorization.source === "environment"}
      >
        <p class="text-xs text-[var(--text-muted)]">
          {t("workCatalog.environmentSource")}
        </p>
      </Show>
      <p class="text-xs text-[var(--text-muted)]">
        {t("workCatalog.writePermission")}
      </p>
    </div>
  );

  return (
    <section
      class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 space-y-3"
      data-testid="catalog-authorization"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <h3 class="font-semibold">{t("workCatalog.authTitle")}</h3>
          <p class="mt-1 text-xs text-[var(--text-muted)]">
            {t("workCatalog.authDescription")}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setCatalogAuthorizationOpen(false)}
          class="text-xs text-[var(--text-muted)]"
        >
          {t("workCatalog.close")}
        </button>
      </div>
      <Show when={props.browserController} fallback={renderOpenApiForm()}>
        {(ctrl) => {
          const live = () => ctrl().browserState();
          const isLoggedIn = () =>
            Boolean(live()?.running && !live()?.isLoginPage);
          return (
            <div class="space-y-3">
              <div
                class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 p-3 space-y-3"
                data-testid="catalog-browser-auth"
              >
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <h4 class="text-xs font-semibold">
                      {t("workCatalog.browserAuthTitle")}
                    </h4>
                    <p class="mt-1 text-xs text-[var(--text-muted)]">
                      {t("workCatalog.browserAuthDescription")}
                    </p>
                  </div>
                  <div class="flex flex-wrap gap-2">
                    <button
                      type="button"
                      disabled={ctrl().busy()}
                      onClick={() => void ctrl().openSellerBackend()}
                      class={secondaryBtnClass}
                    >
                      {t("workCatalog.openSellerBrowser")}
                    </button>
                    <button
                      type="button"
                      disabled={ctrl().busy()}
                      onClick={() => void detectSeller()}
                      class={secondaryBtnClass}
                    >
                      {t("workCatalog.detectSellerAccount")}
                    </button>
                  </div>
                </div>
                <p
                  class={`text-xs ${isLoggedIn() ? "text-emerald-600" : "text-[var(--text-muted)]"}`}
                >
                  {isLoggedIn()
                    ? t("workCatalog.browserLoggedIn", {
                        title: live()?.title || live()?.url || "1688",
                      })
                    : t("workCatalog.browserNeedLogin")}
                </p>
                <div class="flex flex-wrap items-end gap-2">
                  <label class="min-w-[200px] flex-1 space-y-1 text-xs">
                    {t("workCatalog.seller")}
                    <input
                      class={inputClass}
                      value={browserSeller()}
                      onInput={(event) =>
                        setBrowserSeller(event.currentTarget.value)
                      }
                    />
                  </label>
                  <button
                    type="button"
                    disabled={
                      workCatalogState.saving ||
                      ctrl().busy() ||
                      !browserSeller().trim()
                    }
                    onClick={() => void bindBrowser()}
                    class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
                  >
                    {workCatalogState.saving
                      ? t("workCatalog.saving")
                      : t("workCatalog.bindBrowserSeller")}
                  </button>
                </div>
              </div>

              <details
                class="rounded-lg border border-[var(--border-subtle)] p-3"
                open={workCatalogState.status?.authorization.source === "local"}
                data-testid="catalog-advanced-apikey"
              >
                <summary class="cursor-pointer text-xs font-medium select-none">
                  {t("workCatalog.advancedApiKeyTitle")}
                </summary>
                <div class="mt-3">{renderOpenApiForm()}</div>
              </details>
            </div>
          );
        }}
      </Show>
    </section>
  );
}
