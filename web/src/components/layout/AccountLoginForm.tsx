// web/src/components/layout/AccountLoginForm.tsx
import { Show } from "solid-js";
import { Globe, LogIn, X } from "lucide-solid";
import {
  cancelDesktopAuthorization,
  desktopAuth,
  startDesktopAuthorization,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";

interface AccountLoginFormProps {
  onSuccess?: () => void;
}

export default function AccountLoginForm(_props: AccountLoginFormProps) {
  const handleStartOneClick = async () => {
    await startDesktopAuthorization();
  };

  const handleCancelOneClick = () => {
    cancelDesktopAuthorization();
  };

  return (
    <div class="p-4 space-y-3.5">
      {/* Header */}
      <div class="flex items-start gap-2.5">
        <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
          <LogIn size={15} />
        </div>
        <div class="min-w-0 flex-1">
          <div class="text-[13px] font-semibold text-[var(--text-primary)]">
            {t("settings.accountSignInTitle")}
          </div>
          <p class="mt-0.5 text-[11px] leading-relaxed text-[var(--text-muted)]">
            {t("settings.accountSignInDesc")}
          </p>
        </div>
      </div>

      {/* Primary Action: One-Click Official Login */}
      <Show
        when={desktopAuth.isAuthorizing}
        fallback={
          <div class="space-y-2 pt-1">
            <button
              type="button"
              onClick={handleStartOneClick}
              class="w-full inline-flex items-center justify-center gap-2 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-3.5 py-2.5 text-[12px] font-semibold text-[var(--accent-foreground)] shadow-sm hover:shadow transition-all active:scale-[0.99] cursor-pointer"
            >
              <Globe size={14} />
              <span>{t("settings.accountOneClickLoginBtn")}</span>
            </button>
            <p class="text-[11px] text-center text-[var(--text-faint)] leading-tight">
              {t("settings.accountLoginWithBrowserHint")}
            </p>
          </div>
        }
      >
        {/* Waiting for Browser Confirmation Card */}
        <div class="rounded-xl border border-amber-500/30 bg-amber-500/10 p-3 space-y-2.5">
          <div class="flex items-center gap-2 text-amber-500 text-[12px] font-medium">
            <Spinner class="h-3.5 w-3.5" />
            <span class="truncate">{t("settings.accountWaitingBrowserAuth")}</span>
          </div>
          <div class="flex items-center justify-between pt-0.5">
            <span class="text-[11px] text-[var(--text-muted)] font-mono truncate max-w-[170px]">
              {desktopAuth.serverBaseUrl}
            </span>
            <button
              type="button"
              onClick={handleCancelOneClick}
              class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-rose-500 hover:bg-rose-500/10 transition-colors cursor-pointer"
            >
              <X size={11} />
              <span>{t("settings.accountCancelAuthBtn")}</span>
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}
