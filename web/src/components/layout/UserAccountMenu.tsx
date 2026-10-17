// web/src/components/layout/UserAccountMenu.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import {
  Check,
  Cpu,
  GitBranch,
  LogIn,
  LogOut,
  Mail,
  Pencil,
  SlidersHorizontal,
  User,
} from "lucide-solid";
import {
  appSettings,
  hydrateLocalUserIdentity,
  openProviderModal,
  openSettingsModal,
  resolvedUserDisplayName,
  resolvedUserEmail,
  signInUserAccount,
  signOutUserAccount,
  updateAppSetting,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import { Icon } from "../icons";
import { t } from "../../i18n";

export default function UserAccountMenu() {
  const [menuOpen, setMenuOpen] = createSignal(false);
  const [editingProfile, setEditingProfile] = createSignal(false);
  const [draftName, setDraftName] = createSignal("");
  const [draftEmail, setDraftEmail] = createSignal("");
  let containerRef: HTMLDivElement | undefined;

  onMount(() => {
    if (!appSettings.detectedSystemUsername && !appSettings.detectedGitUserEmail) {
      void hoxiAPI
        .getSystemInfo()
        .then((info) => {
          hydrateLocalUserIdentity(info);
        })
        .catch(() => {
          /* ignore */
        });
    }

    const handleDocPointerDown = (e: MouseEvent) => {
      if (!menuOpen()) return;
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setMenuOpen(false);
        setEditingProfile(false);
      }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && menuOpen()) {
        setMenuOpen(false);
        setEditingProfile(false);
      }
    };

    document.addEventListener("mousedown", handleDocPointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      document.removeEventListener("mousedown", handleDocPointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const userInitial = () => {
    const name = resolvedUserDisplayName().trim();
    return name ? name.charAt(0).toUpperCase() : "U";
  };

  const connectedProvidersCount = () => workspace.connectedProviders?.length || 0;

  const toggleMenu = () => {
    const next = !menuOpen();
    setMenuOpen(next);
    if (next) {
      setEditingProfile(false);
      setDraftName(appSettings.userDisplayName || resolvedUserDisplayName());
      setDraftEmail(appSettings.userEmail || resolvedUserEmail());
    }
  };

  const startEditingProfile = () => {
    setDraftName(appSettings.userDisplayName || resolvedUserDisplayName());
    setDraftEmail(appSettings.userEmail || resolvedUserEmail());
    setEditingProfile(true);
  };

  const handleSaveProfile = (e?: Event) => {
    e?.preventDefault();
    updateAppSetting("userDisplayName", draftName().trim());
    updateAppSetting("userEmail", draftEmail().trim());
    setEditingProfile(false);
  };

  const handleSyncGitIdentity = () => {
    const gitName =
      appSettings.detectedGitUserName || appSettings.detectedSystemUsername || "";
    const gitEmail = appSettings.detectedGitUserEmail || "";
    setDraftName(gitName);
    setDraftEmail(gitEmail);
    signInUserAccount(gitName, gitEmail);
    setEditingProfile(false);
  };

  const handleSignInSubmit = (e: Event) => {
    e.preventDefault();
    signInUserAccount(draftName(), draftEmail());
    setMenuOpen(false);
  };

  const handleSignOut = () => {
    signOutUserAccount();
    setEditingProfile(false);
    setMenuOpen(false);
  };

  return (
    <div
      ref={containerRef}
      class="relative mt-auto border-t border-[var(--border-subtle)]/50 pt-2 pb-2.5 px-2 flex items-center justify-between gap-1"
    >
      {/* Upward Kilo-style Account Popover */}
      <Show when={menuOpen()}>
        <div
          class="absolute bottom-full left-2 right-2 mb-2 z-50 rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden animate-in fade-in slide-in-from-bottom-1 duration-150"
          onClick={(e) => e.stopPropagation()}
        >
          <Show
            when={appSettings.userLoggedIn}
            fallback={
              /* Logged-Out Sign In Form */
              <form onSubmit={handleSignInSubmit} class="p-3.5 space-y-3">
                <div class="flex items-start gap-2.5">
                  <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                    <LogIn size={15} />
                  </div>
                  <div class="min-w-0">
                    <div class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                      {t("settings.accountSignInTitle")}
                    </div>
                    <p class="mt-0.5 text-[11px] leading-relaxed text-[var(--text-muted)]">
                      {t("settings.accountSignInDesc")}
                    </p>
                  </div>
                </div>

                <div class="space-y-2">
                  <div>
                    <label class="block text-[10.5px] font-medium text-[var(--text-secondary)] mb-1">
                      {t("settings.accountNameInputLabel")}
                    </label>
                    <input
                      type="text"
                      value={draftName()}
                      onInput={(e) => setDraftName(e.currentTarget.value)}
                      placeholder={
                        appSettings.detectedGitUserName ||
                        appSettings.detectedSystemUsername ||
                        "developer"
                      }
                      class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                    />
                  </div>
                  <div>
                    <label class="block text-[10.5px] font-medium text-[var(--text-secondary)] mb-1">
                      {t("settings.accountEmailInputLabel")}
                    </label>
                    <input
                      type="email"
                      value={draftEmail()}
                      onInput={(e) => setDraftEmail(e.currentTarget.value)}
                      placeholder={
                        appSettings.detectedGitUserEmail || "you@example.com"
                      }
                      class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                    />
                  </div>
                </div>

                <div class="pt-1 flex flex-col gap-1.5">
                  <button
                    type="submit"
                    class="w-full inline-flex items-center justify-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3 py-1.5 text-[12px] font-medium text-white hover:opacity-95 transition-opacity cursor-pointer"
                  >
                    <LogIn size={13} />
                    {t("settings.accountSignInBtn")}
                  </button>

                  <Show
                    when={
                      appSettings.detectedGitUserEmail ||
                      appSettings.detectedGitUserName
                    }
                  >
                    <button
                      type="button"
                      onClick={handleSyncGitIdentity}
                      class="w-full inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    >
                      <GitBranch size={12} />
                      {t("settings.accountUseGitIdentityBtn")}
                    </button>
                  </Show>
                </div>
              </form>
            }
          >
            {/* Logged-In Account Card */}
            <div class="p-3 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/40">
              <div class="flex items-center gap-2.5">
                <div class="relative flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-[var(--accent-primary)] to-indigo-500 text-[13px] font-bold text-white shadow-xs">
                  {userInitial()}
                  <span class="absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[var(--bg-panel)] bg-emerald-500" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-1.5">
                    <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
                      {resolvedUserDisplayName()}
                    </span>
                    <span class="shrink-0 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[9.5px] font-bold tracking-wide text-[var(--accent-primary)]">
                      {t("settings.accountPlanBadge")}
                    </span>
                  </div>
                  <div class="flex items-center gap-1 text-[11px] text-[var(--text-muted)] truncate mt-0.5">
                    <Mail size={10} class="shrink-0 opacity-75" />
                    <span class="truncate font-mono text-[10.5px]">
                      {resolvedUserEmail()}
                    </span>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() =>
                    editingProfile()
                      ? handleSaveProfile()
                      : startEditingProfile()
                  }
                  class="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer shrink-0"
                  title={t("settings.accountEditProfileBtn")}
                >
                  <Show when={editingProfile()} fallback={<Pencil size={12} />}>
                    <Check size={13} class="text-emerald-500" />
                  </Show>
                </button>
              </div>

              {/* Inline Profile Edit Drawer */}
              <Show when={editingProfile()}>
                <form
                  onSubmit={handleSaveProfile}
                  class="mt-3 pt-2.5 border-t border-[var(--border-subtle)]/70 space-y-2"
                >
                  <div>
                    <label class="block text-[10px] font-medium text-[var(--text-muted)] mb-0.5">
                      {t("settings.accountNameInputLabel")}
                    </label>
                    <input
                      type="text"
                      value={draftName()}
                      onInput={(e) => setDraftName(e.currentTarget.value)}
                      placeholder={t("settings.profileNamePlaceholder")}
                      class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                    />
                  </div>
                  <div>
                    <label class="block text-[10px] font-medium text-[var(--text-muted)] mb-0.5">
                      {t("settings.accountEmailInputLabel")}
                    </label>
                    <input
                      type="email"
                      value={draftEmail()}
                      onInput={(e) => setDraftEmail(e.currentTarget.value)}
                      placeholder={t("settings.profileEmailPlaceholder", {
                        email:
                          appSettings.detectedGitUserEmail || "you@example.com",
                      })}
                      class="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] font-mono text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                    />
                  </div>
                  <div class="flex items-center justify-between gap-1.5 pt-0.5">
                    <button
                      type="button"
                      onClick={handleSyncGitIdentity}
                      class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    >
                      <GitBranch size={11} />
                      {t("settings.accountUseGitIdentityBtn")}
                    </button>
                    <button
                      type="submit"
                      class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-95 transition-opacity cursor-pointer"
                    >
                      <Check size={11} />
                      {t("settings.accountSaveProfileBtn")}
                    </button>
                  </div>
                </form>
              </Show>

              {/* Connected Providers Pill */}
              <div class="mt-2.5 flex items-center justify-between rounded-lg bg-[var(--bg-panel)]/80 border border-[var(--border-subtle)]/60 px-2.5 py-1 text-[10.5px] text-[var(--text-muted)]">
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

            {/* Sign Out Footer */}
            <div class="border-t border-[var(--border-subtle)] p-1.5">
              <button
                type="button"
                onClick={handleSignOut}
                class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] font-medium text-rose-500 hover:bg-rose-500/10 transition-colors cursor-pointer"
              >
                <LogOut size={13} />
                <span>{t("settings.accountSignOutBtn")}</span>
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
          when={appSettings.userLoggedIn}
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
          <div class="relative flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-[var(--accent-primary)] to-indigo-500 text-[11.5px] font-bold text-white shadow-2xs">
            {userInitial()}
            <span class="absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-[var(--bg-panel)] bg-emerald-500" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-1.5 leading-tight">
              <span class="truncate text-[12.5px] font-semibold text-[var(--text-primary)]">
                {resolvedUserDisplayName()}
              </span>
              <span class="shrink-0 rounded px-1 py-0.2 text-[9px] font-bold bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                {t("settings.accountPlanBadge")}
              </span>
            </div>
            <div class="truncate font-mono text-[10.5px] text-[var(--text-muted)] leading-tight mt-0.5">
              {resolvedUserEmail()}
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
