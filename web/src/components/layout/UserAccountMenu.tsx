// web/src/components/layout/UserAccountMenu.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import {
  Cpu,
  LogOut,
  Mail,
  SlidersHorizontal,
  User,
} from "lucide-solid";
import {
  desktopAuth,
  logoutDesktopAccount,
  openProviderModal,
  openSettingsModal,
  refreshAuthStatus,
  workspace,
} from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import AccountLoginForm from "./AccountLoginForm";

export default function UserAccountMenu() {
  const [menuOpen, setMenuOpen] = createSignal(false);
  const [signingOut, setSigningOut] = createSignal(false);
  const [signOutFailed, setSignOutFailed] = createSignal(false);
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

  const connectedProvidersCount = () => workspace.connectedProviders?.length || 0;

  const toggleMenu = () => {
    setMenuOpen(!menuOpen());
  };

  const handleSignOut = async () => {
    if (signingOut()) return;
    setSigningOut(true);
    setSignOutFailed(false);
    try {
      await logoutDesktopAccount();
      setMenuOpen(false);
    } catch {
      setSignOutFailed(true);
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <div
      ref={containerRef}
      class="relative mt-auto mr-1 border-t border-[var(--border-subtle)]/50 pt-2 pb-2.5 px-2 flex items-center justify-between gap-1"
    >
      {/* Upward Kilo-style Account Popover */}
      <Show when={menuOpen()}>
        <div
          class="absolute bottom-full left-2 right-2 mb-2 z-50 rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden animate-in fade-in slide-in-from-bottom-1 duration-150"
          onClick={(e) => e.stopPropagation()}
        >
          <Show
            when={isLoggedIn()}
            fallback={<AccountLoginForm onSuccess={() => setMenuOpen(false)} />}
          >
            {/* Logged-In Account Card */}
            <div class="p-3 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/40">
              <div class="flex items-center gap-2.5">
                <div class="relative flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[13px] font-bold text-[var(--accent-foreground)] shadow-xs overflow-hidden">
                  <Show when={userAvatar()} fallback={userInitial()}>
                    <img src={userAvatar()} alt="" class="h-full w-full object-cover" />
                  </Show>
                  <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--bg-panel)] bg-emerald-500" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-1.5">
                    <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
                      {userName()}
                    </span>
                    <span class="shrink-0 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[9.5px] font-bold tracking-wide text-[var(--accent-primary)]">
                      {userPlan()}
                    </span>
                  </div>
                  <div class="flex items-center gap-1 text-[11px] text-[var(--text-muted)] truncate mt-0.5">
                    <Mail size={10} class="shrink-0 opacity-75" />
                    <span class="truncate font-mono text-[10.5px]">
                      {userEmail()}
                    </span>
                  </div>
                </div>
              </div>

              {/* Official Cloud Status Badge */}
              <div class="mt-2.5 flex items-center justify-between rounded-lg bg-emerald-500/10 border border-emerald-500/20 px-2.5 py-1 text-[10.5px] text-emerald-500 font-medium">
                <span class="inline-flex items-center gap-1.5">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                  {t("settings.accountOfficialAccountTitle")}
                </span>
                <span class="font-mono text-[9.5px] opacity-75">Hoxi.ai</span>
              </div>

              {/* Connected Providers Pill */}
              <div class="mt-2 flex items-center justify-between rounded-lg bg-[var(--bg-panel)]/80 border border-[var(--border-subtle)]/60 px-2.5 py-1 text-[10.5px] text-[var(--text-muted)]">
                <span class="inline-flex items-center gap-1.5">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                  {t("settings.accountConnectedProviders", {
                    count: connectedProvidersCount(),
                  })}
                </span>
                <span class="truncate max-w-[110px] font-mono text-[10px] text-[var(--text-faint)]">
                  {workspace.selectedModel?.modelID || "auto"}
                </span>
              </div>
            </div>

            {/* Menu Items */}
            <div class="p-1.5 space-y-0.5">
              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  openProviderModal("providers");
                }}
                class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              >
                <span class="inline-flex items-center gap-2">
                  <Cpu size={13} class="text-[var(--text-muted)]" />
                  {t("models.modalTitle")}
                </span>
              </button>

              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  openSettingsModal("general");
                }}
                class="w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              >
                <span class="inline-flex items-center gap-2">
                  <SlidersHorizontal size={13} class="text-[var(--text-muted)]" />
                  {t("settings.modalTitle")}
                </span>
                <kbd class="text-[10px] font-mono text-[var(--text-faint)]">
                  ⌘,
                </kbd>
              </button>
            </div>
          </Show>
        </div>
      </Show>

      {/* Bottom-Left Trigger: Avatar + Name + Email (Kilo-style) */}
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
                    <div class="truncate text-[12.5px] font-medium text-[var(--text-primary)] leading-tight">
                      {t("settings.accountSignInBtn")}
                    </div>
                    <div class="truncate text-[10.5px] text-[var(--text-faint)] leading-tight mt-0.5">
                      {t("settings.accountGuestBadge")}
                    </div>
                  </div>
                </>
              }
            >
              <div class="relative flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[11.5px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
                <Show when={userAvatar()} fallback={userInitial()}>
                  <img src={userAvatar()} alt="" class="h-full w-full object-cover" />
                </Show>
                <span class="absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-[var(--bg-panel)] bg-emerald-500" />
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-1.5 leading-tight">
                  <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
                    {userName()}
                  </span>
                  <span class="shrink-0 rounded px-1 py-0.2 text-[9px] font-bold bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                    {userPlan()}
                  </span>
                </div>
                <div class="truncate font-mono text-[10.5px] text-[var(--text-muted)] leading-tight mt-0.5">
                  {userEmail()}
                </div>
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
            <div class="truncate text-[10px] text-[var(--text-faint)] leading-tight mt-0.5">
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
