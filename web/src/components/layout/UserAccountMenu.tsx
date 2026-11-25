// web/src/components/layout/UserAccountMenu.tsx
import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  ChevronRight,
  CircleHelp,
  ExternalLink,
  KeyRound,
  LogOut,
  User,
  X,
} from "lucide-solid";
import {
  cancelDesktopAuthorization,
  clearDesktopAuthError,
  desktopAuth,
  desktopUserAvatar,
  desktopUserDisplayName,
  desktopUserEmail,
  desktopUserInitial,
  desktopUserPlan,
  logoutDesktopAccount,
  openDesktopPortalPage,
  openSettingsModal,
  refreshAuthStatus,
  startDesktopAuthorization,
} from "../../store/workspace";
import { Icon, Spinner } from "../icons";
import { t } from "../../i18n";

const menuItemClass =
  "w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer";

export default function UserAccountMenu() {
  const [menuOpen, setMenuOpen] = createSignal(false);
  const [signingOut, setSigningOut] = createSignal(false);
  let containerRef: HTMLDivElement | undefined;

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

  onMount(() => {
    void refreshAuthStatus();
    document.addEventListener("mousedown", handleDocPointerDown);
    window.addEventListener("keydown", handleKeyDown);
  });

  onCleanup(() => {
    document.removeEventListener("mousedown", handleDocPointerDown);
    window.removeEventListener("keydown", handleKeyDown);
  });

  createEffect(() => {
    if (!desktopAuth.isLoggedIn) {
      setMenuOpen(false);
    }
  });

  const isLoggedIn = () => desktopAuth.isLoggedIn;

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
      {/* Transient auth error notice when logged out */}
      <Show when={!isLoggedIn() && !desktopAuth.isAuthorizing && desktopAuth.error}>
        <div
          role="alert"
          class="absolute bottom-full left-1.5 right-1.5 mb-2 z-50 flex items-start gap-2 rounded-xl border border-rose-500/30 bg-[var(--bg-panel)] px-3 py-2 text-[11.5px] text-rose-500 shadow-lg"
        >
          <span class="min-w-0 flex-1 break-words leading-snug">{desktopAuth.error}</span>
          <button
            type="button"
            onClick={() => clearDesktopAuthError()}
            aria-label={t("common.close")}
            class="shrink-0 rounded-md p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          >
            <X size={12} />
          </button>
        </div>
      </Show>

      {/* Upward Floating Account Menu (logged-in only) */}
      <Presence when={isLoggedIn() && menuOpen()}>
        {(state) => (
          <div
            data-state={state()}
            style={{ "--hoxi-origin": "bottom center" }}
            class="hoxi-popover absolute bottom-full left-1.5 right-1.5 mb-2 z-50 rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <div class="p-2.5 space-y-2">
              {/* Identity Header */}
              <div class="flex items-center gap-2.5 px-1.5 py-1">
                <div class="relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[12px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
                  <Show when={desktopUserAvatar()} fallback={desktopUserInitial()}>
                    <img src={desktopUserAvatar()} alt="" class="h-full w-full object-cover" />
                  </Show>
                  <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--bg-panel)] bg-emerald-500" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-1.5">
                    <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                      {desktopUserDisplayName()}
                    </span>
                    <span class="shrink-0 rounded-md bg-[var(--bg-inset)] border border-[var(--border-subtle)] px-1.5 py-0.5 text-[10.5px] font-semibold text-[var(--text-secondary)]">
                      {desktopUserPlan()}
                    </span>
                  </div>
                  <Show when={desktopUserEmail()}>
                    <div class="truncate font-mono text-[10.5px] text-[var(--text-muted)] leading-tight mt-0.5">
                      {desktopUserEmail()}
                    </div>
                  </Show>
                </div>
              </div>

              <div class="border-t border-[var(--border-subtle)]/60" />

              {/* Account-only Actions */}
              <div class="space-y-0.5">
                <button
                  type="button"
                  onClick={() => {
                    setMenuOpen(false);
                    openSettingsModal("account");
                  }}
                  class={menuItemClass}
                >
                  <span class="inline-flex items-center gap-2.5">
                    <User size={14} class="text-[var(--text-muted)] shrink-0" />
                    <span>{t("settings.accountPageTitle")}</span>
                  </span>
                  <ChevronRight size={13} class="text-[var(--text-faint)] shrink-0" />
                </button>

                <button
                  type="button"
                  onClick={() => {
                    setMenuOpen(false);
                    void openDesktopPortalPage("keys");
                  }}
                  class={menuItemClass}
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
                    void openDesktopPortalPage("feedback");
                  }}
                  class={menuItemClass}
                >
                  <span class="inline-flex items-center gap-2.5">
                    <CircleHelp size={14} class="text-[var(--text-muted)] shrink-0" />
                    <span>{t("settings.accountReportIssue")}</span>
                  </span>
                </button>
              </div>

              <Show when={desktopAuth.error}>
                <div class="px-2 text-[11.5px] text-rose-500 break-words">
                  {desktopAuth.error}
                </div>
              </Show>

              <div class="border-t border-[var(--border-subtle)]/60 pt-1">
                <button
                  type="button"
                  disabled={signingOut()}
                  onClick={() => void handleSignOut()}
                  class="w-full flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:bg-rose-500/10 hover:text-rose-500 transition-colors cursor-pointer disabled:opacity-50"
                >
                  <LogOut size={14} class="shrink-0" />
                  <span>{t("settings.accountSignOutBtn")}</span>
                </button>
              </div>
            </div>
          </div>
        )}
      </Presence>

      {/* Bottom-Left Area: Logged-in menu trigger / Direct browser login / Inline authorizing bar */}
      <Show
        when={!isLoggedIn() && desktopAuth.isAuthorizing}
        fallback={
          <Show
            when={isLoggedIn()}
            fallback={
              <button
                type="button"
                onClick={() => void startDesktopAuthorization()}
                class="group flex-1 flex items-center gap-2 rounded-xl px-2 py-1.5 text-left text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer min-w-0"
                title={t("settings.accountSignInTooltip")}
              >
                <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-dashed border-[var(--border-strong)] bg-[var(--bg-inset)] text-[var(--text-muted)]">
                  <User size={12.5} />
                </div>
                <span class="truncate text-[12.5px] font-medium text-[var(--text-secondary)] group-hover:text-[var(--text-primary)] leading-tight">
                  {t("settings.accountSignInBtn")}
                </span>
                <ExternalLink
                  size={11.5}
                  class="ml-auto shrink-0 text-[var(--text-faint)] opacity-60 group-hover:opacity-100 transition-opacity"
                />
              </button>
            }
          >
            <button
              type="button"
              onClick={() => setMenuOpen((v) => !v)}
              class={`flex-1 flex items-center gap-2 rounded-xl px-2 py-1.5 text-left transition-colors cursor-pointer min-w-0 ${
                menuOpen()
                  ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60"
              }`}
              title={t("settings.accountMenuTitle")}
            >
              <div class="relative flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[11px] font-bold text-[var(--accent-foreground)] shadow-2xs overflow-hidden">
                <Show when={desktopUserAvatar()} fallback={desktopUserInitial()}>
                  <img src={desktopUserAvatar()} alt="" class="h-full w-full object-cover" />
                </Show>
                <span class="absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-[var(--bg-panel)] bg-emerald-500" />
              </div>
              <span class="truncate text-[12.5px] font-medium text-[var(--text-primary)]">
                {desktopUserDisplayName()}
              </span>
            </button>
          </Show>
        }
      >
        <div
          class="flex-1 flex items-center gap-2 rounded-xl px-2 py-1.5 min-w-0 bg-amber-500/8 border border-amber-500/20"
          title={desktopAuth.serverBaseUrl}
        >
          <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-500">
            <Spinner class="h-3.5 w-3.5" />
          </div>
          <span class="min-w-0 flex-1 truncate text-[12px] font-medium text-amber-500 leading-tight">
            {t("settings.accountWaitingBrowserShort")}
          </span>
          <button
            type="button"
            onClick={() => void cancelDesktopAuthorization()}
            title={t("settings.accountCancelAuthBtn")}
            aria-label={t("settings.accountCancelAuthBtn")}
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-lg text-[var(--text-muted)] hover:text-rose-500 hover:bg-rose-500/10 transition-colors cursor-pointer"
          >
            <X size={13} />
          </button>
        </div>
      </Show>

      {/* Right Settings Gear Button */}
      <button
        type="button"
        onClick={() => openSettingsModal("general")}
        class="flex items-center justify-center p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer shrink-0"
        title={`${t("settings.modalTitle")} (⌘,)`}
      >
        <Icon name="settings-gear" size="small" />
      </button>
    </div>
  );
}
