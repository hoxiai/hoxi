// web/src/components/layout/UserAccountMenu.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  ChevronRight,
  CircleHelp,
  Cpu,
  Gift,
  Globe,
  KeyRound,
  LogOut,
  Moon,
  SlidersHorizontal,
  Sparkles,
  Sun,
  User,
} from "lucide-solid";
import {
  appSettings,
  desktopAuth,
  logoutDesktopAccount,
  openDesktopPortalPage,
  openProviderModal,
  openSettingsModal,
  performDesktopCheckin,
  refreshAuthStatus,
  refreshDesktopMember,
  updateAppSetting,
  type ThemeMode,
} from "../../store/workspace";
import { Icon } from "../icons";
import { locale, t, toggleLocale } from "../../i18n";
import AccountLoginForm from "./AccountLoginForm";

function formatPointsBadge(points: number): string {
  if (!Number.isFinite(points) || points <= 0) return "0";
  if (Number.isInteger(points)) return points.toLocaleString();
  return points.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  });
}

export default function UserAccountMenu() {
  const [menuOpen, setMenuOpen] = createSignal(false);
  const [signingOut, setSigningOut] = createSignal(false);
  let containerRef: HTMLDivElement | undefined;

  onMount(() => {
    void refreshAuthStatus();

    const handleDocPointerDown = (e: MouseEvent) => {
      if (!menuOpen()) return;
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && menuOpen()) {
        setMenuOpen(false);
      }
    };

    document.addEventListener("mousedown", handleDocPointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      document.removeEventListener("mousedown", handleDocPointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const isLoggedIn = () => desktopAuth.isLoggedIn;
  const userName = () =>
    (desktopAuth.user?.nickname || desktopAuth.user?.username || "Developer").trim();
  const userEmail = () => desktopAuth.user?.email || "";
  const userAvatar = () => desktopAuth.user?.avatar || desktopAuth.user?.avatarUrl || "";
  const userPlan = () => desktopAuth.user?.plan || t("settings.accountPlanBadge");

  const userInitial = () => {
    const name = userName();
    return name ? name.charAt(0).toUpperCase() : "U";
  };

  const toggleMenu = () => {
    const next = !menuOpen();
    setMenuOpen(next);
    if (next && isLoggedIn()) {
      void refreshDesktopMember();
    }
  };

  const handleCheckinClick = async (e: MouseEvent) => {
    e.stopPropagation();
    await performDesktopCheckin();
  };

  const cycleThemeMode = (e: MouseEvent) => {
    e.stopPropagation();
    const order: ThemeMode[] = ["light", "dark", "system"];
    const idx = order.indexOf(appSettings.themeMode);
    const next = order[(idx + 1) % order.length] || "light";
    updateAppSetting("themeMode", next);
  };

  const currentThemeLabel = () => {
    if (appSettings.themeMode === "dark") return t("settings.themeDark");
    if (appSettings.themeMode === "system") return t("settings.themeSystem");
    return t("settings.themeLight");
  };

  const currentLangLabel = () =>
    locale() === "zh-CN" ? t("settings.langZhLabel") : t("settings.langEnLabel");

  const formatPointsNum = (n: number): string => {
    const safe = Math.max(0, Math.trunc(Number(n || 0)));
    return safe.toLocaleString("en-US");
  };

  const formatRangeLabel = (
    range: { min: number; max: number } | undefined,
    fallback: number,
  ): string => {
    const min = Number(range?.min || 0);
    const max = Number(range?.max || 0);
    if (min > 0 && max > min) {
      return `${formatPointsNum(min)}~${formatPointsNum(max)}`;
    }
    return formatPointsNum(max > 0 ? max : fallback);
  };

  const checkinSubtitle = () => {
    const m = desktopAuth.member;
    if (m.nextMilestone && m.nextMilestone.day > 0) {
      return t("settings.accountCheckinMilestoneDesc", {
        streak: m.streak,
        day: m.nextMilestone.day,
        bonus: m.nextMilestone.points,
      });
    }
    return t("settings.accountCheckinStreakDesc", {
      streak: m.streak,
      next: formatRangeLabel(m.nextPointsRange, m.nextPoints),
    });
  };

  const handleSignOut = async () => {
    if (signingOut()) return;
    setSigningOut(true);
    try {
      await logoutDesktopAccount();
      setMenuOpen(false);
    } catch {
      /* error stored in desktopAuth.error */
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <div
      ref={containerRef}
      class="relative mt-auto mr-1 border-t border-[var(--border-subtle)]/50 pt-2 pb-2.5 px-2 flex items-center justify-between gap-1.5"
    >
      {/* Upward Floating Account & Check-in Popover */}
      <Presence when={menuOpen()}>
        {(state) => (
          <div
            data-state={state()}
            style={{ "--hoxi-origin": "bottom center" }}
            class="hoxi-popover absolute bottom-full left-1.5 right-1.5 mb-2 z-50 rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <Show
              when={isLoggedIn()}
              fallback={<AccountLoginForm onSuccess={() => setMenuOpen(false)} />}
            >
              <div class="p-3 space-y-2.5">
                {/* 1. Top Identity + Points Balance Pill */}
                <div class="flex items-center justify-between gap-2">
                  <div class="flex items-center gap-2 min-w-0 flex-1">
                    <div class="relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[12px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
                      <Show when={userAvatar()} fallback={userInitial()}>
                        <img src={userAvatar()} alt="" class="h-full w-full object-cover" />
                      </Show>
                      <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--bg-panel)] bg-emerald-500" />
                    </div>
                    <div class="min-w-0 flex-1">
                      <div class="flex items-center gap-1.5">
                        <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                          {userName()}
                        </span>
                        <span class="shrink-0 rounded-md bg-[var(--bg-inset)] border border-[var(--border-subtle)] px-1.5 py-0.5 text-[10.5px] font-semibold text-[var(--text-secondary)]">
                          {userPlan()}
                        </span>
                      </div>
                      <Show when={userEmail()}>
                        <div class="truncate font-mono text-[10.5px] text-[var(--text-muted)] leading-tight mt-0.5">
                          {userEmail()}
                        </div>
                      </Show>
                    </div>
                  </div>

                  {/* Right Points Balance Pill */}
                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      void openDesktopPortalPage("billing");
                    }}
                    title={t("settings.accountPointsTooltip")}
                    class="shrink-0 inline-flex items-center gap-1 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[12px] font-mono font-semibold tabular-nums text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <Sparkles size={12} class="text-amber-500 shrink-0" />
                    <span>{formatPointsBadge(desktopAuth.member.points)}</span>
                  </button>
                </div>

                {/* 2. Embedded Daily Check-in Card */}
                <div class="rounded-xl bg-[var(--bg-inset)]/90 border border-[var(--border-subtle)]/70 px-3 py-2.5 flex items-center justify-between gap-2.5">
                  <div class="min-w-0 flex-1">
                    <div class="text-[13px] font-bold text-[var(--text-primary)] leading-tight truncate">
                      {desktopAuth.member.checkedInToday
                        ? t("settings.accountCheckedInTitle", {
                            points: formatPointsNum(desktopAuth.member.todayPoints),
                          })
                        : t("settings.accountCheckinTitle", {
                            points: formatRangeLabel(
                              desktopAuth.member.todayPointsRange,
                              desktopAuth.member.todayPoints,
                            ),
                          })}
                    </div>
                    <div class="text-[11px] text-[var(--text-muted)] leading-tight mt-1 truncate">
                      {checkinSubtitle()}
                    </div>
                  </div>

                  <button
                    type="button"
                    disabled={
                      desktopAuth.member.checkingIn || desktopAuth.member.checkedInToday
                    }
                    onClick={handleCheckinClick}
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
                </div>

                <Show when={desktopAuth.member.error}>
                  <div class="px-1 text-[11px] text-rose-500 truncate">
                    {desktopAuth.member.error}
                  </div>
                </Show>

                {/* 3. Action & Preference Menu List */}
                <div class="space-y-0.5 pt-0.5">
                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      void openDesktopPortalPage("rewards");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <Gift size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.accountRewardsEntry")}</span>
                    </span>
                    <ChevronRight size={13} class="text-[var(--text-faint)] shrink-0" />
                  </button>

                  <div class="my-1 border-t border-[var(--border-subtle)]/60" />

                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      void openDesktopPortalPage("dashboard");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <User size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.accountManageAccount")}</span>
                    </span>
                    <ChevronRight size={13} class="text-[var(--text-faint)] shrink-0" />
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      void openDesktopPortalPage("keys");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <KeyRound size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.accountApiKeysEntry")}</span>
                    </span>
                    <ChevronRight size={13} class="text-[var(--text-faint)] shrink-0" />
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      openProviderModal("providers");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <Cpu size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("models.modalTitle")}</span>
                    </span>
                    <ChevronRight size={13} class="text-[var(--text-faint)] shrink-0" />
                  </button>

                  <div class="my-1 border-t border-[var(--border-subtle)]/60" />

                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      toggleLocale();
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <Globe size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.sectionLanguageTitle")}</span>
                    </span>
                    <span class="inline-flex items-center gap-1 text-[11.5px] text-[var(--text-muted)]">
                      <span>{currentLangLabel()}</span>
                      <ChevronRight size={13} class="text-[var(--text-faint)]" />
                    </span>
                  </button>

                  <button
                    type="button"
                    onClick={cycleThemeMode}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <Show
                        when={appSettings.themeMode === "dark"}
                        fallback={<Sun size={14} class="text-[var(--text-muted)] shrink-0" />}
                      >
                        <Moon size={14} class="text-[var(--text-muted)] shrink-0" />
                      </Show>
                      <span>{t("settings.sectionThemeTitle")}</span>
                    </span>
                    <span class="inline-flex items-center gap-1 text-[11.5px] text-[var(--text-muted)]">
                      <span>{currentThemeLabel()}</span>
                      <ChevronRight size={13} class="text-[var(--text-faint)]" />
                    </span>
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      openSettingsModal("general");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <SlidersHorizontal size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.modalTitle")}</span>
                    </span>
                    <kbd class="text-[11px] font-mono text-[var(--text-faint)]">
                      ⌘,
                    </kbd>
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      void openDesktopPortalPage("feedback");
                    }}
                    class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  >
                    <span class="inline-flex items-center gap-2.5">
                      <CircleHelp size={14} class="text-[var(--text-muted)] shrink-0" />
                      <span>{t("settings.accountReportIssue")}</span>
                    </span>
                  </button>
                </div>

                {/* 4. Full-Width Sign Out Button */}
                <button
                  type="button"
                  disabled={signingOut()}
                  onClick={handleSignOut}
                  class="w-full flex items-center justify-center gap-1.5 rounded-xl bg-[var(--bg-inset)]/80 hover:bg-rose-500/10 hover:text-rose-500 border border-[var(--border-subtle)]/70 py-2 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer disabled:opacity-50"
                >
                  <LogOut size={13} class="shrink-0" />
                  <span>{t("settings.accountSignOutBtn")}</span>
                </button>
              </div>
            </Show>
          </div>
        )}
      </Presence>

      {/* Bottom-Left Trigger: Avatar + Name + Plan Badge */}
      <button
        type="button"
        onClick={toggleMenu}
        class={`flex-1 flex items-center gap-2 rounded-xl px-2 py-1.5 text-left transition-colors cursor-pointer min-w-0 ${
          menuOpen()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60"
        }`}
        title={t("settings.accountMenuTitle")}
      >
        <Show
          when={desktopAuth.isAuthorizing}
          fallback={
            <Show
              when={isLoggedIn()}
              fallback={
                <>
                  <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-dashed border-[var(--border-strong)] bg-[var(--bg-inset)] text-[var(--text-muted)]">
                    <User size={13} />
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-[13px] font-medium text-[var(--text-primary)] leading-tight">
                      {t("settings.accountSignInBtn")}
                    </div>
                  </div>
                </>
              }
            >
              <div class="relative flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[12px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
                <Show when={userAvatar()} fallback={userInitial()}>
                  <img src={userAvatar()} alt="" class="h-full w-full object-cover" />
                </Show>
                <span class="absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-[var(--bg-panel)] bg-emerald-500" />
              </div>
              <div class="min-w-0 flex-1 flex items-center justify-between gap-1.5">
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                    {userName()}
                  </span>
                  <span class="shrink-0 rounded-md px-1.5 py-0.2 text-[10.5px] font-semibold bg-[var(--bg-inset)] border border-[var(--border-subtle)] text-[var(--text-secondary)]">
                    {userPlan()}
                  </span>
                </div>
                <span class="shrink-0 inline-flex items-center gap-1 rounded-full bg-[var(--bg-inset)] border border-[var(--border-subtle)]/70 px-1.5 py-0.5 text-[10.5px] font-mono font-semibold tabular-nums text-[var(--text-secondary)]">
                  <Sparkles size={10} class="text-amber-500 shrink-0" />
                  <span>{formatPointsBadge(desktopAuth.member.points)}</span>
                </span>
              </div>
            </Show>
          }
        >
          <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-500/10 text-amber-500 border border-amber-500/30">
            <span class="relative flex h-2 w-2">
              <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
              <span class="relative inline-flex rounded-full h-2 w-2 bg-amber-500" />
            </span>
          </div>
          <div class="min-w-0 flex-1">
            <div class="truncate text-[12px] font-medium text-amber-500 leading-tight">
              {t("settings.accountWaitingBrowserShort")}
            </div>
            <div class="truncate text-[11px] text-[var(--text-faint)] leading-tight mt-0.5">
              {t("settings.accountCancelAuthBtn")}
            </div>
          </div>
        </Show>
      </button>

      {/* Right Settings Gear Button */}
      <button
        type="button"
        onClick={() => openSettingsModal("general")}
        class="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer shrink-0"
        title={`${t("settings.modalTitle")} (⌘,)`}
      >
        <Icon name="settings-gear" size="small" />
      </button>
    </div>
  );
}
