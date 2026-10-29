// web/src/components/settings/AccountSettingsTab.tsx
// Dedicated Hoxi account overview inside Settings. The bottom-left account
// popover stays the quick surface; this tab is the stable, deep-linkable page
// for identity, membership points and account actions. It reads the same
// auth-state store so there is a single source of truth.
import { createSignal, onMount, Show } from "solid-js";
import {
  ChevronRight,
  CircleHelp,
  CreditCard,
  Cpu,
  Flame,
  Gift,
  KeyRound,
  LogOut,
  RefreshCw,
  Sparkles,
  User,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  desktopAuth,
  desktopCheckinSubtitle,
  desktopUserAvatar,
  desktopUserDisplayName,
  desktopUserEmail,
  desktopUserInitial,
  desktopUserPlan,
  formatMemberPoints,
  logoutDesktopAccount,
  openDesktopPortalPage,
  openProviderModal,
  performDesktopCheckin,
  refreshAuthStatus,
  refreshDesktopMember,
  workspace,
} from "../../store/workspace";
import AccountLoginForm from "../layout/AccountLoginForm";
import { SettingGroup, SettingRow } from "./setting-controls";

const actionRowClass =
  "w-full flex items-center justify-between gap-3 px-5 py-3 text-left text-[13px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer";

export function AccountSettingsTab() {
  const [signingOut, setSigningOut] = createSignal(false);
  const [refreshing, setRefreshing] = createSignal(false);

  onMount(() => {
    void refreshAuthStatus();
  });

  const isLoggedIn = () => desktopAuth.isLoggedIn;

  const handleRefresh = async () => {
    if (refreshing()) return;
    setRefreshing(true);
    try {
      await refreshAuthStatus();
      await refreshDesktopMember();
    } finally {
      setRefreshing(false);
    }
  };

  const handleCheckin = async () => {
    await performDesktopCheckin();
  };

  const handleSignOut = async () => {
    if (signingOut()) return;
    setSigningOut(true);
    try {
      await logoutDesktopAccount();
    } catch {
      /* error stored in desktopAuth.error */
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <div class="space-y-6">
      {/* Header */}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
        <div class="flex items-center gap-3 min-w-0">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <User size={16} />
          </div>
          <div class="min-w-0">
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("settings.accountPageTitle")}
            </h3>
            <p class="text-[12px] text-[var(--text-secondary)]">
              {t("settings.accountPageSubtitle")}
            </p>
          </div>
        </div>
        <Show when={isLoggedIn()}>
          <button
            type="button"
            disabled={refreshing()}
            onClick={() => void handleRefresh()}
            title={t("settings.accountRefreshTooltip")}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer disabled:opacity-50"
          >
            <RefreshCw size={13} class={refreshing() ? "animate-spin" : ""} />
            <span>{t("settings.accountRefreshBtn")}</span>
          </button>
        </Show>
      </div>

      <Show
        when={isLoggedIn()}
        fallback={
          <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35">
            <AccountLoginForm />
          </div>
        }
      >
        {/* Identity */}
        <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 px-5 py-4">
          <div class="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[15px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
            <Show when={desktopUserAvatar()} fallback={desktopUserInitial()}>
              <img src={desktopUserAvatar()} alt="" class="h-full w-full object-cover" />
            </Show>
            <span class="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-[var(--bg-panel)] bg-emerald-500" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="truncate text-[14px] font-semibold text-[var(--text-primary)]">
                {desktopUserDisplayName()}
              </span>
              <span class="shrink-0 rounded-md bg-[var(--bg-panel)] border border-[var(--border-subtle)] px-1.5 py-0.5 text-[10.5px] font-semibold text-[var(--text-secondary)]">
                {desktopUserPlan()}
              </span>
            </div>
            <Show when={desktopUserEmail()}>
              <div class="truncate font-mono text-[11px] text-[var(--text-muted)] mt-0.5">
                {desktopUserEmail()}
              </div>
            </Show>
          </div>
        </div>

        {/* Membership & points */}
        <SettingGroup title={t("settings.accountMembershipTitle")}>
          <SettingRow icon={Sparkles} label={t("settings.accountPointsBalanceLabel")}>
            <button
              type="button"
              onClick={() => void openDesktopPortalPage("billing")}
              title={t("settings.accountPointsTooltip")}
              class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1 font-mono text-[13px] font-semibold tabular-nums text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Sparkles size={12} class="text-amber-500 shrink-0" />
              <span>{formatMemberPoints(desktopAuth.member.points)}</span>
            </button>
          </SettingRow>

          <SettingRow icon={CreditCard} label={t("settings.accountPendingPointsLabel")}>
            <span class="font-mono text-[13px] font-semibold tabular-nums text-[var(--text-secondary)]">
              {formatMemberPoints(desktopAuth.member.pendingPoints)}
            </span>
          </SettingRow>

          <SettingRow
            icon={Flame}
            label={t("settings.accountStreakLabel")}
            tip={desktopCheckinSubtitle()}
          >
            <span class="font-mono text-[13px] font-medium tabular-nums text-[var(--text-secondary)]">
              {t("settings.accountStreakValue", { streak: desktopAuth.member.streak })}
            </span>
            <button
              type="button"
              disabled={desktopAuth.member.checkingIn || desktopAuth.member.checkedInToday}
              onClick={() => void handleCheckin()}
              class={`shrink-0 rounded-lg px-3 py-1.5 text-[12px] font-semibold transition-all ${
                desktopAuth.member.checkedInToday
                  ? "bg-[var(--bg-panel)] text-[var(--text-muted)] border border-[var(--border-subtle)] cursor-default"
                  : "bg-[var(--text-primary)] text-[var(--bg-panel)] hover:opacity-90 active:scale-95 cursor-pointer shadow-2xs"
              }`}
            >
              {desktopAuth.member.checkingIn
                ? t("settings.accountCheckingInBtn")
                : desktopAuth.member.checkedInToday
                ? t("settings.accountCheckedInBtn")
                : t("settings.accountCheckinBtn")}
            </button>
          </SettingRow>
        </SettingGroup>

        <Show when={desktopAuth.member.error}>
          <div class="px-1 text-[12px] text-rose-500">{desktopAuth.member.error}</div>
        </Show>

        {/* Account actions */}
        <SettingGroup title={t("settings.accountActionsTitle")}>
          <button
            type="button"
            onClick={() => void openDesktopPortalPage("rewards")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <Gift size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountRewardsEntry")}</span>
            </span>
            <ChevronRight size={14} class="text-[var(--text-faint)] shrink-0" />
          </button>

          <button
            type="button"
            onClick={() => void openDesktopPortalPage("account")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <User size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountManageAccount")}</span>
            </span>
            <ChevronRight size={14} class="text-[var(--text-faint)] shrink-0" />
          </button>

          <button
            type="button"
            onClick={() => void openDesktopPortalPage("keys")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <KeyRound size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountApiKeysEntry")}</span>
            </span>
            <ChevronRight size={14} class="text-[var(--text-faint)] shrink-0" />
          </button>

          <button
            type="button"
            onClick={() => void openDesktopPortalPage("billing")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <CreditCard size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountBillingEntry")}</span>
            </span>
            <ChevronRight size={14} class="text-[var(--text-faint)] shrink-0" />
          </button>

          <button
            type="button"
            onClick={() => openProviderModal("providers")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <Cpu size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountManageProvidersEntry")}</span>
            </span>
            <span class="inline-flex items-center gap-1 text-[11.5px] text-[var(--text-muted)]">
              <span>{t("settings.accountConnectedProviders", { count: workspace.connectedProviders.length })}</span>
              <ChevronRight size={14} class="text-[var(--text-faint)]" />
            </span>
          </button>

          <button
            type="button"
            onClick={() => void openDesktopPortalPage("feedback")}
            class={actionRowClass}
          >
            <span class="inline-flex items-center gap-2.5">
              <CircleHelp size={15} class="text-[var(--text-muted)] shrink-0" />
              <span>{t("settings.accountReportIssue")}</span>
            </span>
            <ChevronRight size={14} class="text-[var(--text-faint)] shrink-0" />
          </button>
        </SettingGroup>

        {/* Sign out */}
        <button
          type="button"
          disabled={signingOut()}
          onClick={() => void handleSignOut()}
          class="w-full flex items-center justify-center gap-1.5 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 hover:bg-rose-500/10 hover:text-rose-500 py-2.5 text-[12.5px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer disabled:opacity-50"
        >
          <LogOut size={14} class="shrink-0" />
          <span>{t("settings.accountSignOutBtn")}</span>
        </button>
      </Show>
    </div>
  );
}
