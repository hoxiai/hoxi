// web/src/components/layout/UserAccountMenu.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  ChevronRight,
  CircleHelp,
  KeyRound,
  LogOut,
  User,
} from "lucide-solid";
import {
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
} from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import AccountLoginForm from "./AccountLoginForm";

const menuItemClass =
  "w-full flex items-center justify-between rounded-lg px-2.5 py-1.5 text-left text-[12.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer";

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
      {/* Upward Floating Minimal Account Popover */}
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
            </Show>
          </div>
        )}
      </Presence>

      {/* Bottom-Left Trigger: Avatar + Display Name only */}
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
        <Show
          when={desktopAuth.isAuthorizing}
          fallback={
            <Show
              when={isLoggedIn()}
              fallback={
                <>
                  <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-dashed border-[var(--border-strong)] bg-[var(--bg-inset)] text-[var(--text-muted)]">
                    <User size={12.5} />
                  </div>
                  <span class="truncate text-[12.5px] font-medium text-[var(--text-secondary)] leading-tight">
                    {t("settings.accountSignInBtn")}
                  </span>
                </>
              }
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
            </Show>
          }
        >
          <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-amber-500/10 text-amber-500 border border-amber-500/30">
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
        class="flex items-center justify-center p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer shrink-0"
        title={`${t("settings.modalTitle")} (⌘,)`}
      >
        <Icon name="settings-gear" size="small" />
      </button>
    </div>
  );
}
