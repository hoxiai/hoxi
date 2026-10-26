// web/src/components/settings/StorageCleanupSection.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { Database, FileText, FolderArchive, HardDrive, RefreshCw, Sparkles } from "lucide-solid";
import type { SystemStorageStats } from "../../services/api";
import { cleanSystemStorage, fetchSystemStorage } from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";
import { SettingGroup } from "./setting-controls";

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIdx = 0;
  while (value >= 1024 && unitIdx < units.length - 1) {
    value /= 1024;
    unitIdx++;
  }
  const decimals = unitIdx >= 2 ? (value >= 100 ? 0 : 1) : 0;
  return `${value.toFixed(decimals)} ${units[unitIdx]}`;
}

export function StorageCleanupSection(props: { onToast: (msg: string) => void }) {
  const [storage, setStorage] = createSignal<SystemStorageStats | null>(null);
  const [loading, setLoading] = createSignal(false);
  const [cleaning, setCleaning] = createSignal(false);

  const loadStorage = async () => {
    setLoading(true);
    try {
      setStorage(await fetchSystemStorage());
    } catch {
      /* ignore */
    } finally {
      setLoading(false);
    }
  };

  onMount(() => {
    void loadStorage();
  });

  const handleClean = async () => {
    if (cleaning()) return;
    setCleaning(true);
    try {
      const res = await cleanSystemStorage();
      setStorage(res.storage);
      if (res.freedBytes > 0) {
        props.onToast(t("settings.storageCleanSuccess", { size: formatBytes(res.freedBytes) }));
      } else {
        props.onToast(t("settings.storageCleanAlreadyOptimal"));
      }
    } catch (err) {
      props.onToast(
        t("settings.storageCleanFailed", {
          error: err instanceof Error ? err.message : String(err),
        })
      );
    } finally {
      setCleaning(false);
    }
  };

  const categories = () => {
    const s = storage();
    return [
      {
        key: "database",
        label: t("settings.storageCatDatabase"),
        desc: t("settings.storageCatDatabaseDesc"),
        bytes: s?.databaseBytes ?? 0,
        color: "bg-[var(--accent-primary)]",
        dotColor: "bg-[var(--accent-primary)]",
        icon: Database,
      },
      {
        key: "sessions",
        label: t("settings.storageCatSessions"),
        desc: t("settings.storageCatSessionsDesc"),
        bytes: s?.sessionsBytes ?? 0,
        color: "bg-emerald-500",
        dotColor: "bg-emerald-500",
        icon: HardDrive,
      },
      {
        key: "engineLogs",
        label: t("settings.storageCatEngineLogs"),
        desc: t("settings.storageCatEngineLogsDesc"),
        bytes: s?.engineLogsBytes ?? 0,
        color: "bg-amber-500",
        dotColor: "bg-amber-500",
        icon: FileText,
      },
      {
        key: "cacheIndex",
        label: t("settings.storageCatCacheIndex"),
        desc: t("settings.storageCatCacheIndexDesc"),
        bytes: s?.cacheIndexBytes ?? 0,
        color: "bg-violet-500",
        dotColor: "bg-violet-500",
        icon: FolderArchive,
      },
    ];
  };

  const totalBytes = () => storage()?.totalBytes ?? 0;

  return (
    <SettingGroup
      title={t("settings.sectionStorageTitle")}
      tip={t("settings.sectionStorageDesc")}
      action={
        <button
          type="button"
          disabled={loading() || cleaning()}
          onClick={() => void loadStorage()}
          class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
          title={t("common.refresh")}
        >
          <RefreshCw size={14} class={loading() ? "animate-spin" : ""} />
        </button>
      }
    >
      {/* Summary Header & One-Click Clean Button */}
      <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
        <div class="space-y-1 min-w-0">
          <div class="flex flex-wrap items-center gap-2.5">
            <span class="text-[12px] font-medium text-[var(--text-muted)]">
              {t("settings.storageTotalLabel")}
            </span>
            <span class="font-mono text-[18px] font-semibold tracking-tight text-[var(--text-primary)]">
              {storage() ? formatBytes(totalBytes()) : "—"}
            </span>
            <span class="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/25 bg-emerald-500/10 px-2.5 py-0.5 text-[11px] font-medium text-emerald-500">
              <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
              {t("settings.storageAutoCleanBadge")}
            </span>
          </div>
          <Show when={storage()?.dataDir}>
            <div
              class="font-mono text-[11.5px] text-[var(--text-faint)] truncate selectable-text"
              title={storage()?.dataDir}
            >
              {storage()?.dataDir}
            </div>
          </Show>
        </div>

        <button
          type="button"
          disabled={cleaning() || loading()}
          onClick={() => void handleClean()}
          class="hoxi-press inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-4 py-2 text-[13px] font-medium text-[var(--accent-foreground)] shadow-2xs transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-wait shrink-0"
        >
          <Show when={cleaning()} fallback={<Sparkles size={14} />}>
            <Spinner class="h-3.5 w-3.5" />
          </Show>
          <span>
            {cleaning() ? t("settings.storageCleaningBtn") : t("settings.storageCleanBtn")}
          </span>
        </button>
      </div>

      {/* Stacked Breakdown Bar */}
      <Show when={totalBytes() > 0}>
        <div class="px-5 pb-3">
          <div class="flex h-2 w-full overflow-hidden rounded-full bg-[var(--bg-inset)] p-0.5 gap-0.5">
            <For each={categories()}>
              {(cat) => {
                const pct = () => Math.max(0, (cat.bytes / Math.max(1, totalBytes())) * 100);
                return (
                  <Show when={cat.bytes > 0}>
                    <div
                      class={`h-full rounded-full transition-all duration-300 ${cat.color}`}
                      style={{ width: `${Math.max(2, pct())}%` }}
                      title={`${cat.label}: ${formatBytes(cat.bytes)}`}
                    />
                  </Show>
                );
              }}
            </For>
          </div>
        </div>
      </Show>

      {/* Category Rows */}
      <div class="divide-y divide-[var(--border-subtle)]/55 border-t border-[var(--border-subtle)]/55">
        <For each={categories()}>
          {(cat) => {
            const Icon = cat.icon;
            return (
              <div class="flex items-center justify-between gap-4 px-5 py-3">
                <div class="flex items-center gap-3 min-w-0">
                  <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)]">
                    <Icon size={15} />
                  </div>
                  <div class="min-w-0">
                    <div class="flex items-center gap-2">
                      <span class={`h-2 w-2 rounded-full shrink-0 ${cat.dotColor}`} />
                      <span class="text-[13px] font-medium text-[var(--text-primary)] truncate">
                        {cat.label}
                      </span>
                    </div>
                    <div class="text-[11.5px] text-[var(--text-muted)] truncate">
                      {cat.desc}
                    </div>
                  </div>
                </div>
                <span class="font-mono text-[13px] font-medium text-[var(--text-secondary)] shrink-0">
                  {storage() ? formatBytes(cat.bytes) : "—"}
                </span>
              </div>
            );
          }}
        </For>
      </div>
    </SettingGroup>
  );
}
