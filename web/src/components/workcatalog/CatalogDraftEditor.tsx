import { Show } from "solid-js";
import { t } from "../../i18n";
import {
  closeCatalogDetail,
  importCatalogDraft,
  saveCatalogDraft,
  updateCatalogEditor,
  workCatalogState,
} from "../../store/work-catalog";

const fieldClass =
  "w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-2 text-sm outline-none focus:border-[var(--accent-primary)]";

export default function CatalogDraftEditor() {
  return (
    <Show when={workCatalogState.editor}>
      <section class="space-y-4" data-testid="catalog-draft-editor">
        <div class="flex items-center justify-between gap-3">
          <h3 class="font-semibold">
            {workCatalogState.editor?.mode === "create"
              ? t("workCatalog.newDraft")
              : t("workCatalog.updateDraft")}
          </h3>
          <button
            class="text-xs text-[var(--text-muted)]"
            onClick={closeCatalogDetail}
          >
            {t("workCatalog.close")}
          </button>
        </div>
        <Show when={workCatalogState.editor?.mode === "create"}>
          <p class="rounded-lg bg-amber-500/10 p-3 text-xs text-[var(--text-secondary)]">
            {t("workCatalog.newProductLimit")}
          </p>
          <label class="inline-flex cursor-pointer rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs">
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
          <p class="text-xs text-[var(--text-muted)]">
            {t("workCatalog.importHint")}
          </p>
        </Show>
        <form
          class="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            void saveCatalogDraft();
          }}
        >
          <label class="block space-y-1 text-xs">
            {t("workCatalog.subject")}
            <input
              required
              class={fieldClass}
              value={workCatalogState.editor?.subject || ""}
              onInput={(event) =>
                updateCatalogEditor("subject", event.currentTarget.value)
              }
            />
          </label>
          <p class="text-xs text-[var(--text-muted)]">
            {t("workCatalog.titleLimit")}
          </p>
          <label class="block space-y-1 text-xs">
            {t("workCatalog.description")}
            <textarea
              rows={7}
              class={fieldClass}
              value={workCatalogState.editor?.description || ""}
              onInput={(event) =>
                updateCatalogEditor("description", event.currentTarget.value)
              }
            />
          </label>
          <label class="block space-y-1 text-xs">
            {t("workCatalog.images")}
            <textarea
              rows={4}
              class={`${fieldClass} font-mono text-xs`}
              value={workCatalogState.editor?.images || ""}
              onInput={(event) =>
                updateCatalogEditor("images", event.currentTarget.value)
              }
            />
          </label>
          <Show when={workCatalogState.editor?.mode === "create"}>
            <label class="block space-y-1 text-xs">
              {t("workCatalog.categoryId")}
              <input
                class={fieldClass}
                inputmode="numeric"
                value={workCatalogState.editor?.categoryId || ""}
                onInput={(event) =>
                  updateCatalogEditor("categoryId", event.currentTarget.value)
                }
              />
            </label>
            <label class="block space-y-1 text-xs">
              {t("workCatalog.sourceUrl")}
              <input
                class={fieldClass}
                value={workCatalogState.editor?.sourceUrl || ""}
                onInput={(event) =>
                  updateCatalogEditor("sourceUrl", event.currentTarget.value)
                }
              />
            </label>
          </Show>
          <div class="flex gap-2">
            <button
              type="submit"
              disabled={workCatalogState.saving}
              class="rounded-lg bg-[var(--accent-primary)] px-3 py-2 text-xs font-semibold text-white disabled:opacity-50"
            >
              {workCatalogState.saving
                ? t("workCatalog.saving")
                : t("workCatalog.saveDraft")}
            </button>
            <button
              type="button"
              onClick={closeCatalogDetail}
              class="rounded-lg border border-[var(--border-subtle)] px-3 py-2 text-xs"
            >
              {t("workCatalog.cancel")}
            </button>
          </div>
        </form>
      </section>
    </Show>
  );
}
