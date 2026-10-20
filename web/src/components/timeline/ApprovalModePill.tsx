// web/src/components/timeline/ApprovalModePill.tsx
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  Check,
  ChevronDown,
  Shield,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
} from "lucide-solid";
import {
  appSettings,
  openProviderModal,
  setApprovalMode,
  type ApprovalMode,
} from "../../store/workspace";
import { t } from "../../i18n";

interface ApprovalModeOption {
  id: ApprovalMode;
  labelKey:
    | "approval.modeAutoLabel"
    | "approval.modeSmartLabel"
    | "approval.modeManualLabel";
  shortKey:
    | "approval.modeAutoShort"
    | "approval.modeSmartShort"
    | "approval.modeManualShort";
  descKey:
    | "approval.modeAutoDesc"
    | "approval.modeSmartDesc"
    | "approval.modeManualDesc";
  badgeKey?: "approval.modeAutoBadge";
}

const MODE_OPTIONS: ApprovalModeOption[] = [
  {
    id: "auto",
    labelKey: "approval.modeAutoLabel",
    shortKey: "approval.modeAutoShort",
    descKey: "approval.modeAutoDesc",
    badgeKey: "approval.modeAutoBadge",
  },
  {
    id: "smart",
    labelKey: "approval.modeSmartLabel",
    shortKey: "approval.modeSmartShort",
    descKey: "approval.modeSmartDesc",
  },
  {
    id: "manual",
    labelKey: "approval.modeManualLabel",
    shortKey: "approval.modeManualShort",
    descKey: "approval.modeManualDesc",
  },
];

function renderModeIcon(mode: ApprovalMode, size = 12.5) {
  if (mode === "auto") return <ShieldCheck size={size} class="shrink-0" />;
  if (mode === "smart") return <Shield size={size} class="shrink-0" />;
  return <ShieldAlert size={size} class="shrink-0" />;
}

export default function ApprovalModePill() {
  const [open, setOpen] = createSignal(false);
  let buttonRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  const currentMode = (): ApprovalMode => appSettings.approvalMode || "auto";

  const currentShortLabel = () => {
    const mode = currentMode();
    if (mode === "auto") return t("approval.modeAutoShort");
    if (mode === "smart") return t("approval.modeSmartShort");
    return t("approval.modeManualShort");
  };

  const isDefaultAuto = () => currentMode() === "auto";

  createEffect(() => {
    if (!open()) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        buttonRef &&
        !buttonRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handleClick);
    window.addEventListener("keydown", handleKey);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handleClick);
      window.removeEventListener("keydown", handleKey);
    });
  });

  const onSelect = (mode: ApprovalMode) => {
    setApprovalMode(mode);
    setOpen(false);
  };

  return (
    <div class="relative inline-block select-none">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center rounded-lg transition-colors cursor-pointer ${
          isDefaultAuto() ? "p-1.5" : "gap-1 px-2 py-1 text-[12px] font-medium"
        } ${
          open()
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : currentMode() === "manual"
              ? "bg-[var(--warn-surface)] text-[var(--warn-text)] border border-[var(--warn-border)] hover:brightness-105"
              : currentMode() === "auto"
                ? "text-[var(--ok-text)] hover:bg-[var(--bg-hover)]"
                : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={t("approval.modeTooltip", { mode: currentShortLabel() })}
        aria-label={t("approval.modeTooltip", { mode: currentShortLabel() })}
      >
        <span
          class={
            currentMode() === "manual"
              ? "text-[var(--warn-text)]"
              : currentMode() === "auto"
                ? "text-[var(--ok-text)]"
                : "text-[var(--accent-primary)]"
          }
        >
          {renderModeIcon(currentMode(), 12.5)}
        </span>
        <Show when={!isDefaultAuto()}>
          <span>{currentShortLabel()}</span>
          <ChevronDown
            size={10.5}
            class={`opacity-75 transition-transform duration-200 ${
              open() ? "rotate-180" : ""
            }`}
          />
        </Show>
      </button>

      <Presence when={open()}>
        {(state) => (
        <div
          ref={menuRef}
          data-state={state()}
          style={{ "--hoxi-origin": "bottom right" }}
          class="hoxi-popover absolute bottom-full right-0 mb-2 w-[296px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 z-50"
        >
          <div class="px-2.5 py-1.5 flex items-center justify-between border-b border-[var(--border-subtle)] mb-1">
            <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              {t("approval.modeMenuTitle")}
            </span>
            <span class="rounded-md bg-[var(--warn-surface)] border border-[var(--warn-border)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--warn-text)]">
              {t("approval.modeMenuBadge")}
            </span>
          </div>

          <div class="space-y-1">
            <For each={MODE_OPTIONS}>
              {(opt) => {
                const isSelected = () => currentMode() === opt.id;
                return (
                  <button
                    type="button"
                    onClick={() => onSelect(opt.id)}
                    class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer ${
                      isSelected()
                        ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <div
                      class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                        isSelected()
                          ? opt.id === "manual"
                            ? "bg-[var(--warn-text)] text-white"
                            : opt.id === "auto"
                              ? "bg-[var(--ok-text)] text-white"
                              : "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                          : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                      }`}
                    >
                      {renderModeIcon(opt.id, 12)}
                    </div>

                    <div class="flex-1 min-w-0">
                      <div class="flex items-center justify-between gap-1.5">
                        <span
                          class={`text-[12px] font-medium ${
                            isSelected()
                              ? "text-[var(--text-primary)] font-semibold"
                              : ""
                          }`}
                        >
                          {t(opt.labelKey)}
                        </span>
                        <Show when={opt.badgeKey}>
                          <span class="rounded px-1.5 py-[1px] text-[11px] font-medium bg-[var(--ok-surface)] text-[var(--ok-text)] border border-[var(--ok-border)]">
                            {t(opt.badgeKey!)}
                          </span>
                        </Show>
                      </div>
                      <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
                        {t(opt.descKey)}
                      </p>
                    </div>

                    <Show when={isSelected()}>
                      <Check
                        size={14}
                        class="shrink-0 text-[var(--accent-primary)] mt-1"
                      />
                    </Show>
                  </button>
                );
              }}
            </For>
          </div>

          <div class="mt-1 pt-1 border-t border-[var(--border-subtle)]">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                openProviderModal("policy");
              }}
              class="w-full flex items-center gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <SlidersHorizontal size={12} class="text-[var(--accent-primary)] shrink-0" />
              <span class="truncate">{t("approval.openPolicySettings")}</span>
            </button>
          </div>
        </div>
        )}
      </Presence>
    </div>
  );
}
