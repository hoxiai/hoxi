// web/src/components/timeline/ToolMissingMediaCard.tsx
import { createMemo, Show } from "solid-js";
import { KeyRound, Sparkles, WandSparkles } from "lucide-solid";
import { t } from "../../i18n";
import type { MessagePart } from "../../services/sse-types";
import {
  desktopAuth,
  dispatchSessionPrompt,
  openProviderModal,
  openSettingsModal,
  workspace,
} from "../../store/workspace";

type ToolPart = Extract<MessagePart, { type: "tool" }>;
export type MissingMediaCapability = "image" | "video" | "speech";

/**
 * Detects whether a failed media generation tool call failed specifically
 * because neither the active session model nor any configured local provider
 * supports the requested media modality (avoiding false positives on normal
 * network/rate-limit errors).
 */
export function resolveMissingMediaCapability(
  part: ToolPart
): MissingMediaCapability | null {
  if (!part || part.state.status !== "error") return null;
  const metaCap = part.state.metadata?.missingCapability;
  if (metaCap === "image" || metaCap === "video" || metaCap === "speech") {
    return metaCap;
  }
  const toolLower = (part.tool || "").toLowerCase();
  if (
    toolLower !== "hoxi_image" &&
    toolLower !== "hoxi_video" &&
    toolLower !== "hoxi_speech"
  ) {
    return null;
  }
  const errText = String(part.state.error || "").toLowerCase();
  if (
    !errText.includes("neither the active model nor any configured local provider") &&
    !errText.includes("no image generation model is configured") &&
    !errText.includes("no video generation model") &&
    !errText.includes("supports speech synthesis")
  ) {
    return null;
  }
  if (toolLower === "hoxi_video") return "video";
  if (toolLower === "hoxi_speech") return "speech";
  return "image";
}

export default function ToolMissingMediaCard(props: { part: ToolPart }) {
  const missingCap = createMemo(() => resolveMissingMediaCapability(props.part));

  const hintText = createMemo(() => {
    const cap = missingCap();
    if (cap === "video") return t("toolCard.missingVideoModelHint");
    if (cap === "speech") return t("toolCard.missingSpeechModelHint");
    return t("toolCard.missingImageModelHint");
  });

  const handleConfigureLocal = (e: MouseEvent) => {
    e.stopPropagation();
    openProviderModal(workspace.providers.length > 0 ? "models" : "providers");
  };

  const handleCloudTools = (e: MouseEvent) => {
    e.stopPropagation();
    if (!desktopAuth.isLoggedIn) {
      openSettingsModal("account");
      return;
    }
    void dispatchSessionPrompt(
      t("toolCard.cloudToolsFallbackPrompt"),
      props.part.sessionID
    );
  };

  const handleSvgFallback = (e: MouseEvent) => {
    e.stopPropagation();
    void dispatchSessionPrompt(
      t("toolCard.svgFallbackPrompt"),
      props.part.sessionID
    );
  };

  return (
    <Show when={missingCap()}>
      {(cap) => (
        <div
          class="ml-3 mt-1.5 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3 py-2 text-[12px]"
          onClick={(e) => e.stopPropagation()}
        >
          <span class="text-[var(--text-secondary)]">{hintText()}</span>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              onClick={handleConfigureLocal}
              class="inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
            >
              <KeyRound size={11} />
              <span>{t("toolCard.configureLocalMediaModel")}</span>
            </button>

            <button
              type="button"
              onClick={handleCloudTools}
              class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <Sparkles size={11} />
              <span>
                {desktopAuth.isLoggedIn
                  ? t("toolCard.continueWithCloudTools")
                  : t("toolCard.signInForCloudTools")}
              </span>
            </button>

            <Show when={cap() === "image"}>
              <button
                type="button"
                onClick={handleSvgFallback}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              >
                <WandSparkles size={11} />
                <span>{t("toolCard.continueWithSvgFallback")}</span>
              </button>
            </Show>
          </div>
        </div>
      )}
    </Show>
  );
}
