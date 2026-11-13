import { createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import {
  checkCatalogAuthorization,
  configureCatalogAuthorization,
  setCatalogAuthorizationOpen,
  workCatalogState,
} from "../../store/work-catalog";

const inputClass =
  "w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-2 text-sm outline-none focus:border-[var(--accent-primary)]";

export default function CatalogAuthorization() {
  const [seller, setSeller] = createSignal(
    workCatalogState.status?.authorization.sellerLoginId || "",
  );
  const [appKey, setAppKey] = createSignal(
    workCatalogState.status?.authorization.appKey || "",
  );
  const [secret, setSecret] = createSignal("");
  const [token, setToken] = createSignal("");
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
              !workCatalogState.status?.authorization.configured
            }
            onClick={() => void checkCatalogAuthorization()}
            class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs disabled:opacity-50"
          >
            {t("workCatalog.checkAuthorization")}
          </button>
          <Show when={workCatalogState.status?.authorization.verifiedRead}>
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
    </section>
  );
}
