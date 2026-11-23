// web/src/components/settings/CompanionSettingsSection.tsx
import { Check, ImagePlus, RotateCcw, Sparkles, Trash2 } from "lucide-solid";
import { createSignal, For, Show } from "solid-js";
import { t, type TxKey } from "../../i18n";
import {
  clearCustomCompanionStandee,
  companionState,
  resetCompanionDesktopPosition,
  setCompanionCornerDock,
  setCompanionEnabled,
  setCompanionPersona,
  setCompanionSpeechBubble,
  setCompanionWelcomeStage,
  uploadCustomCompanionStandee,
  type CompanionPersona,
} from "../../store/workspace";
import { CompanionAvatar } from "../companion/companion-art";
import { HelpTip, SettingGroup, SettingRow, ToggleSwitch } from "./setting-controls";

const PERSONA_CARDS: Array<{ id: CompanionPersona; labelKey: TxKey }> = [
  { id: "hoxi-chan", labelKey: "companion.personaHoxiChan" },
  { id: "neko-bot", labelKey: "companion.personaNekoBot" },
  { id: "custom", labelKey: "companion.personaCustom" },
];

export function CompanionSettingsSection() {
  const [errorMsg, setErrorMsg] = createSignal("");
  let standeeInputRef: HTMLInputElement | undefined;

  const onStandeeSelected = async (ev: Event) => {
    const input = ev.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    setErrorMsg("");
    const res = await uploadCustomCompanionStandee(file);
    if (!res.ok) {
      if (res.reason === "invalid_type") setErrorMsg(t("companion.errInvalidType"));
      else if (res.reason === "too_large") setErrorMsg(t("companion.errTooLarge"));
      else setErrorMsg(t("companion.errStorage"));
    }
  };

  const onClearStandee = async () => {
    setErrorMsg("");
    await clearCustomCompanionStandee();
  };

  return (
    <SettingGroup
      title={t("companion.sectionTitle")}
      tip={t("companion.sectionDesc")}
    >
      <SettingRow
        icon={Sparkles}
        label={t("companion.enableLabel")}
        tip={t("companion.enableDesc")}
      >
        <ToggleSwitch
          checked={companionState.enabled}
          onChange={(val) => setCompanionEnabled(val)}
        />
      </SettingRow>

      <Show when={companionState.enabled}>
        <div class="px-5 py-4 space-y-4">
          <div class="space-y-2">
            <div class="text-[12px] font-medium text-[var(--text-secondary)]">
              {t("companion.personaLabel")}
            </div>
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-2.5">
              <For each={PERSONA_CARDS}>
                {(card) => {
                  const active = () => companionState.persona === card.id;
                  return (
                    <button
                      type="button"
                      onClick={() => setCompanionPersona(card.id)}
                      class={`flex items-center gap-3 rounded-xl border p-3 text-left transition-all cursor-pointer ${
                        active()
                          ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/45 ring-2 ring-[var(--accent-primary)]/20"
                          : "border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 hover:border-[var(--border-strong)]"
                      }`}
                    >
                      <CompanionAvatar
                        persona={card.id}
                        mood="idle"
                        customUrl={companionState.customAvatarObjectUrl}
                        size="mini"
                      />
                      <span class="min-w-0 flex-1 truncate text-[12.5px] font-medium text-[var(--text-primary)]">
                        {t(card.labelKey)}
                      </span>
                      <Show when={active()}>
                        <Check size={14} class="shrink-0 text-[var(--accent-primary)]" />
                      </Show>
                    </button>
                  );
                }}
              </For>
            </div>
          </div>

          {/* Custom Transparent Standee Upload (shown when custom persona or custom standee active) */}
          <Show when={companionState.persona === "custom" || Boolean(companionState.customAvatarObjectUrl)}>
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 p-3.5 space-y-2.5">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="flex items-center gap-2 min-w-0">
                  <span class="text-[13px] font-medium text-[var(--text-primary)] truncate">
                    {t("companion.customStandeeTitle")}
                  </span>
                  <HelpTip text={t("companion.customStandeeDesc")} />
                  <Show when={Boolean(companionState.customAvatarObjectUrl)}>
                    <span class="shrink-0 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-text)]">
                      {t("companion.customStandeeActive")}
                    </span>
                  </Show>
                </div>

                <div class="flex items-center gap-2 shrink-0">
                  <input
                    ref={standeeInputRef}
                    type="file"
                    accept="image/png,image/webp,image/gif,image/jpeg"
                    class="hidden"
                    onChange={(ev) => void onStandeeSelected(ev)}
                  />
                  <button
                    type="button"
                    onClick={() => standeeInputRef?.click()}
                    class="hoxi-press inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <ImagePlus size={14} class="text-[var(--accent-primary)]" />
                    <span>{t("companion.uploadStandeeBtn")}</span>
                  </button>

                  <Show when={Boolean(companionState.customAvatarObjectUrl) || companionState.persona === "custom"}>
                    <button
                      type="button"
                      onClick={() => void onClearStandee()}
                      class="hoxi-press inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--danger-text)] hover:border-[var(--danger-border)] cursor-pointer"
                    >
                      <Trash2 size={13} />
                      <span>{t("companion.clearStandeeBtn")}</span>
                    </button>
                  </Show>
                </div>
              </div>

              <Show when={errorMsg()}>
                <div class="text-[12px] font-medium text-[var(--danger-text)]">{errorMsg()}</div>
              </Show>
            </div>
          </Show>
        </div>

        <SettingRow
          label={t("companion.welcomeStageLabel")}
          tip={t("companion.welcomeStageDesc")}
        >
          <ToggleSwitch
            checked={companionState.showWelcomeStage}
            onChange={(val) => setCompanionWelcomeStage(val)}
          />
        </SettingRow>

        <SettingRow
          label={t("companion.cornerDockLabel")}
          tip={t("companion.cornerDockDesc")}
        >
          <div class="flex items-center gap-2.5">
            <Show when={companionState.nativeDesktopSupported && companionState.showCornerDock}>
              <button
                type="button"
                onClick={() => void resetCompanionDesktopPosition()}
                class="hoxi-press inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <RotateCcw size={12} />
                <span>{t("companion.resetPositionBtn")}</span>
              </button>
            </Show>
            <ToggleSwitch
              checked={companionState.showCornerDock}
              onChange={(val) => setCompanionCornerDock(val)}
            />
          </div>
        </SettingRow>

        <SettingRow
          label={t("companion.speechBubbleLabel")}
          tip={t("companion.speechBubbleDesc")}
        >
          <ToggleSwitch
            checked={companionState.showSpeechBubble}
            onChange={(val) => setCompanionSpeechBubble(val)}
          />
        </SettingRow>
      </Show>
    </SettingGroup>
  );
}
