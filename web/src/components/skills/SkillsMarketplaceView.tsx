// web/src/components/skills/SkillsMarketplaceView.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  AlertCircle,
  ArrowUpRight,
  Download,
  LoaderCircle,
  RefreshCw,
  Search,
  ShieldCheck,
  Store,
} from "lucide-solid";
import { t } from "../../i18n";
import type { MarketplaceSkill } from "../../services/api";
import {
  clearSkillInstallNotice,
  installSkillFromMarketplace,
  loadSkillMarketplace,
  skillsStore,
} from "../../store/workspace";

function hostOf(rawUrl: string): string {
  try {
    return new URL(rawUrl).host;
  } catch {
    return rawUrl;
  }
}

function badgeClass(tone?: string): string {
  switch (tone) {
    case "positive":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-500";
    case "warning":
      return "border-amber-500/30 bg-amber-500/10 text-amber-500";
    case "info":
      return "border-sky-500/30 bg-sky-500/10 text-sky-500";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]";
  }
}

export default function SkillsMarketplaceView() {
  const [query, setQuery] = createSignal("");
  const [pendingOverwrite, setPendingOverwrite] = createSignal<MarketplaceSkill | null>(null);

  createEffect(() => {
    void loadSkillMarketplace();
  });

  const filtered = createMemo(() => {
    const q = query().trim().toLowerCase();
    const list = skillsStore.marketplace;
    if (!q) return list;
    return list.filter(
      (item) =>
        item.name.toLowerCase().includes(q) ||
        item.slug.toLowerCase().includes(q) ||
        (item.description || "").toLowerCase().includes(q) ||
        (item.categoryLabel || "").toLowerCase().includes(q)
    );
  });

  const runInstall = async (item: MarketplaceSkill, overwrite: boolean) => {
    setPendingOverwrite(null);
    const ok = await installSkillFromMarketplace(item.slug, { overwrite });
    if (ok) {
      await loadSkillMarketplace({ forceRefresh: true });
    }
  };

  const handleInstall = (item: MarketplaceSkill) => {
    // A local copy with a different digest may contain user edits, so
    // replacing it always needs an explicit second confirmation.
    if (item.installed) {
      setPendingOverwrite(item);
      return;
    }
    void runInstall(item, false);
  };

  return (
    <div class="space-y-3">
      <div class="flex items-start gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[11px] text-[var(--text-muted)]">
        <ShieldCheck size={12} class="mt-0.5 shrink-0 text-[var(--accent-primary)]" />
        <span>{t("skills.marketplaceHint")}</span>
      </div>

      <Show when={skillsStore.installNotice}>
        <div class="flex items-start justify-between gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[11px] text-[var(--text-secondary)]">
          <span>{skillsStore.installNotice}</span>
          <button
            type="button"
            onClick={() => clearSkillInstallNotice()}
            class="shrink-0 text-[var(--text-faint)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            ×
          </button>
        </div>
      </Show>

      <Show when={pendingOverwrite()}>
        {(item) => (
          <div class="rounded-xl border border-amber-500/30 bg-amber-500/10 p-3 space-y-2">
            <div class="text-xs font-semibold text-[var(--text-primary)]">
              {t("skills.overwriteTitle", { name: item().name })}
            </div>
            <p class="text-[11px] leading-relaxed text-[var(--text-secondary)]">
              {t("skills.overwriteBody")}
            </p>
            <div class="flex items-center justify-end gap-1.5">
              <button
                type="button"
                onClick={() => setPendingOverwrite(null)}
                class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11px] text-[var(--text-primary)] hover:border-[var(--border-strong)] cursor-pointer"
              >
                {t("skills.cancel")}
              </button>
              <button
                type="button"
                onClick={() => void runInstall(item(), true)}
                class="rounded-md bg-[var(--accent-primary)] px-2 py-1 text-[11px] font-medium text-[var(--accent-foreground)] cursor-pointer"
              >
                {t("skills.overwriteConfirm")}
              </button>
            </div>
          </div>
        )}
      </Show>

      <div class="flex items-center justify-between gap-2">
        <div class="relative flex min-w-0 flex-1 items-center">
          <Search size={13} class="absolute left-2.5 text-[var(--text-faint)] pointer-events-none" />
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder={t("skills.searchPlaceholder")}
            class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] pl-8 pr-3 py-1.5 text-xs text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
        </div>
        <button
          type="button"
          onClick={() => void loadSkillMarketplace({ forceRefresh: true })}
          disabled={skillsStore.marketplaceLoading}
          class="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
          title={t("skills.refresh")}
        >
          <RefreshCw
            size={12}
            class={skillsStore.marketplaceLoading ? "animate-spin" : ""}
          />
          <span>{skillsStore.marketplaceLoading ? t("skills.refreshing") : t("skills.refresh")}</span>
        </button>
      </div>

      <Show when={skillsStore.marketplaceSource}>
        <div class="px-1 text-[11px] text-[var(--text-faint)]">
          {t("skills.marketplaceSource", { host: hostOf(skillsStore.marketplaceSource) })}
        </div>
      </Show>

      <Show
        when={!skillsStore.marketplaceLoading || skillsStore.marketplace.length > 0}
        fallback={
          <div class="flex flex-col items-center justify-center py-10 text-xs text-[var(--text-muted)] gap-2">
            <LoaderCircle size={18} class="animate-spin text-[var(--accent-primary)]" />
            <span>{t("skills.marketplaceLoading")}</span>
          </div>
        }
      >
        <Show
          when={!skillsStore.marketplaceError}
          fallback={
            <div class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-xs text-[var(--danger-text)] flex items-start gap-2">
              <AlertCircle size={15} class="shrink-0 mt-0.5" />
              <span>{skillsStore.marketplaceError}</span>
            </div>
          }
        >
          <Show
            when={filtered().length > 0}
            fallback={
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 text-center text-xs text-[var(--text-muted)]">
                {skillsStore.marketplace.length === 0
                  ? t("skills.marketplaceEmpty")
                  : t("skills.emptyFiltered")}
              </div>
            }
          >
            <div class="space-y-1.5">
              <For each={filtered()}>
                {(item) => {
                  const installing = () => skillsStore.installingSlug === item.slug;
                  return (
                    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2.5 hover:border-[var(--border-strong)] transition-colors">
                      <div class="flex items-start justify-between gap-2">
                        <div class="min-w-0 flex-1">
                          <div class="flex items-center gap-1.5 flex-wrap">
                            <span class="font-mono text-xs font-semibold text-[var(--text-primary)] truncate">
                              /{item.name}
                            </span>
                            <Show when={item.version}>
                              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-faint)]">
                                v{item.version}
                              </span>
                            </Show>
                            <Show when={item.badge}>
                              <span
                                class={`rounded border px-1.5 py-0.5 text-[11px] font-medium ${badgeClass(
                                  item.badgeTone
                                )}`}
                              >
                                {item.badge}
                              </span>
                            </Show>
                            <Show when={item.installed && !item.updateAvailable}>
                              <span class="rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[11px] font-medium text-emerald-500">
                                {t("skills.installedBadge")}
                              </span>
                            </Show>
                            <Show when={item.updateAvailable}>
                              <span class="rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[11px] font-medium text-amber-500">
                                {t("skills.localDiffers")}
                              </span>
                            </Show>
                          </div>
                          <Show when={item.description}>
                            <p class="mt-1 line-clamp-2 text-[12px] leading-relaxed text-[var(--text-secondary)]">
                              {item.description}
                            </p>
                          </Show>
                          <Show when={item.categoryLabel}>
                            <div class="mt-1 truncate font-mono text-[11px] text-[var(--text-faint)]">
                              {item.categoryLabel}
                              <Show when={item.author}>
                                <span> · {item.author}</span>
                              </Show>
                            </div>
                          </Show>
                        </div>

                        <div class="flex shrink-0 items-center gap-1">
                          <Show when={item.repoUrl || item.homepageUrl}>
                            <a
                              href={item.repoUrl || item.homepageUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                              title={item.repoUrl || item.homepageUrl}
                            >
                              <ArrowUpRight size={13} />
                            </a>
                          </Show>
                          <button
                            type="button"
                            onClick={() => void handleInstall(item)}
                            disabled={installing()}
                            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11px] font-medium text-[var(--text-primary)] transition-colors hover:border-[var(--accent-border)] disabled:opacity-50 cursor-pointer disabled:cursor-default"
                          >
                            <Show
                              when={installing()}
                              fallback={<Download size={11} />}
                            >
                              <LoaderCircle size={11} class="animate-spin" />
                            </Show>
                            <span>
                              {installing()
                                ? t("skills.installing")
                                : item.installed
                                  ? t("skills.reinstall")
                                  : t("skills.install")}
                            </span>
                          </button>
                        </div>
                      </div>
                    </div>
                  );
                }}
              </For>
            </div>
          </Show>
        </Show>
      </Show>

      <div class="flex items-center justify-center gap-1.5 pt-1 text-[11px] text-[var(--text-faint)]">
        <Store size={11} />
        <span>hoxi.ai</span>
      </div>
    </div>
  );
}
