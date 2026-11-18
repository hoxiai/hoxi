// web/src/components/companion/CompanionCornerDock.tsx
import { Minimize2, ShieldAlert } from "lucide-solid";
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import {
  appSettings,
  CELEBRATE_WINDOW_MS,
  companionState,
  deriveCompanionSnapshot,
  focusFirstAttentionSession,
  POKE_BUBBLE_WINDOW_MS,
  pokeCompanion,
  selectSession,
  setCompanionMinimized,
  syncCompanionToDesktopHost,
  workspace,
} from "../../store/workspace";
import {
  CompanionAvatar,
  resolveCompanionDialogue,
  resolveCompanionMoodBadge,
  resolveCompanionPersonaName,
} from "./companion-art";

const RASTERIZED_SVG_CACHE = new Map<string, string>();
const MAX_SVG_CACHE_ENTRIES = 24;

function readThemeCssVar(name: string, fallback: string): string {
  if (typeof document === "undefined" || typeof getComputedStyle !== "function") {
    return fallback;
  }
  const val = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return val || fallback;
}

async function rasterizeSvgElementToPngDataUrl(svgEl: SVGSVGElement | null): Promise<string> {
  if (
    !svgEl ||
    typeof XMLSerializer === "undefined" ||
    typeof Image === "undefined" ||
    typeof document === "undefined"
  ) {
    return "";
  }
  const accent = readThemeCssVar("--accent-primary", "#2563eb");
  const textPrimary = readThemeCssVar("--text-primary", "#0f172a");
  const textSecondary = readThemeCssVar("--text-secondary", "#475569");
  const bgElevated = readThemeCssVar("--bg-elevated", "#f8fafc");
  const borderStrong = readThemeCssVar("--border-strong", "rgba(30,41,59,0.2)");

  const clone = svgEl.cloneNode(true) as SVGSVGElement;
  clone.setAttribute("xmlns", "http://www.w3.org/2000/svg");
  clone.setAttribute("width", "264");
  clone.setAttribute("height", "264");

  let serialized = new XMLSerializer().serializeToString(clone);
  serialized = serialized
    .replace(/var\(--accent-primary\)/g, accent)
    .replace(/var\(--text-primary\)/g, textPrimary)
    .replace(/var\(--text-secondary\)/g, textSecondary)
    .replace(/var\(--bg-elevated\)/g, bgElevated)
    .replace(/var\(--border-strong\)/g, borderStrong);

  const svgDataUrl = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(serialized)}`;
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const size = 264;
        const canvas = document.createElement("canvas");
        canvas.width = size;
        canvas.height = size;
        const ctx = canvas.getContext("2d");
        if (!ctx) {
          resolve("");
          return;
        }
        ctx.drawImage(img, 0, 0, size, size);
        resolve(canvas.toDataURL("image/png"));
      } catch {
        resolve("");
      }
    };
    img.onerror = () => resolve("");
    img.src = svgDataUrl;
  });
}

export default function CompanionCornerDock(props: { hiddenByStage?: boolean }) {
  const [clockTick, setClockTick] = createSignal(Date.now());
  let offscreenSvgHostRef: HTMLDivElement | undefined;

  createEffect(() => {
    const pokeAt = companionState.lastPokeAt;
    const activeSid = workspace.activeSessionID;
    const finishedAt = activeSid ? workspace.sessionFinishedAt[activeSid] || 0 : 0;
    const now = Date.now();
    let nextWakeMs = 60_000;
    if (pokeAt > 0 && now - pokeAt < POKE_BUBBLE_WINDOW_MS) {
      nextWakeMs = Math.min(nextWakeMs, POKE_BUBBLE_WINDOW_MS - (now - pokeAt) + 40);
    }
    if (finishedAt > 0 && now - finishedAt < CELEBRATE_WINDOW_MS) {
      nextWakeMs = Math.min(nextWakeMs, CELEBRATE_WINDOW_MS - (now - finishedAt) + 40);
    }
    const timer = setTimeout(() => setClockTick(Date.now()), Math.max(200, nextWakeMs));
    onCleanup(() => clearTimeout(timer));
  });

  const snap = createMemo(() => deriveCompanionSnapshot(clockTick()));
  const badge = createMemo(() => resolveCompanionMoodBadge(snap()));
  const dialogue = createMemo(() =>
    resolveCompanionDialogue(snap(), appSettings.colorScheme)
  );

  const isUrgentGate = () =>
    snap().mood === "gate-danger" || snap().mood === "gate-approval";

  const shouldShowBubble = () => {
    if (!companionState.showSpeechBubble) return false;
    if (isUrgentGate()) return true;
    if (snap().mood !== "idle") return true;
    return snap().isPoked;
  };

  // Sync live state & standee art to the native floating desktop pet outside the window.
  createEffect(() => {
    const visible = Boolean(companionState.enabled && companionState.showCornerDock);
    const minimized = Boolean(companionState.minimized);
    const darkTheme = workspace.theme === "dark";
    const scheme = appSettings.colorScheme;
    const persona = companionState.persona;
    const customObjUrl = companionState.customAvatarObjectUrl;
    const customDataUrl = companionState.customAvatarDataUrl;
    const curSnap = snap();
    const curBadge = badge().label;
    const curName = resolveCompanionPersonaName(persona);
    const bubbleOn = shouldShowBubble();
    const curDialogue = bubbleOn ? dialogue() : "";
    const levelText = t("companion.levelBadge", { level: curSnap.tokenLevel });
    const accentHex = readThemeCssVar("--accent-primary", "#2563eb");

    const useCustomArt = persona === "custom" && Boolean(customDataUrl || customObjUrl);
    const avatarKey = !visible
      ? ""
      : useCustomArt
        ? `custom:${customObjUrl || "stored"}:${customDataUrl.length}`
        : `svg:${persona}:${curSnap.mood}:${scheme}:${darkTheme ? "dark" : "light"}`;

    void syncCompanionToDesktopHost(
      {
        visible,
        minimized,
        darkTheme,
        persona,
        personaName: curName,
        mood: curSnap.mood,
        badgeLabel: curBadge,
        levelText,
        dialogue: curDialogue,
        showBubble: bubbleOn,
        targetSessionID: curSnap.targetSessionID || "",
        accentHex,
        avatarKey,
      },
      async () => {
        if (useCustomArt) {
          return customDataUrl;
        }
        const cached = RASTERIZED_SVG_CACHE.get(avatarKey);
        if (cached) return cached;
        // Wait one microtask so the offscreen SVG reflects the latest reactive mood/theme.
        await Promise.resolve();
        const svgEl = offscreenSvgHostRef?.querySelector("svg") ?? null;
        const pngUrl = await rasterizeSvgElementToPngDataUrl(svgEl);
        if (pngUrl) {
          if (RASTERIZED_SVG_CACHE.size >= MAX_SVG_CACHE_ENTRIES) {
            const oldest = RASTERIZED_SVG_CACHE.keys().next().value;
            if (oldest) RASTERIZED_SVG_CACHE.delete(oldest);
          }
          RASTERIZED_SVG_CACHE.set(avatarKey, pngUrl);
        }
        return pngUrl;
      }
    );
  });

  // Only render the in-window fallback when running in a standalone web browser
  // without the native floating desktop pet window.
  const shouldRenderWebFallback = () =>
    !companionState.nativeDesktopSupported &&
    companionState.enabled &&
    companionState.showCornerDock &&
    !props.hiddenByStage;

  const onAvatarActivate = () => {
    if (isUrgentGate()) {
      const target = snap().targetSessionID;
      if (target) {
        void selectSession(target);
      } else {
        focusFirstAttentionSession();
      }
      return;
    }
    pokeCompanion();
    setClockTick(Date.now());
  };

  return (
    <>
      <Show when={companionState.enabled && companionState.showCornerDock && companionState.persona !== "custom"}>
        <div
          ref={offscreenSvgHostRef}
          aria-hidden="true"
          class="pointer-events-none fixed -left-[9999px] -top-[9999px] h-24 w-24 overflow-hidden opacity-0"
        >
          <CompanionAvatar
            persona={companionState.persona}
            mood={snap().mood}
            size="stage"
          />
        </div>
      </Show>

      <Show when={shouldRenderWebFallback()}>
        <div class="pointer-events-none fixed bottom-5 right-5 z-30 flex flex-col items-end gap-2 w-max max-w-[276px]">
          <Show
            when={!companionState.minimized || isUrgentGate()}
            fallback={
              <button
                type="button"
                onClick={() => setCompanionMinimized(false)}
                title={t("companion.expandTooltip")}
                aria-label={t("companion.expandTooltip")}
                class="pointer-events-auto hoxi-press hoxi-companion-glass relative inline-flex h-12 w-12 items-center justify-center rounded-full p-1.5 cursor-pointer"
              >
                <CompanionAvatar
                  persona={companionState.persona}
                  mood={snap().mood}
                  customUrl={companionState.customAvatarObjectUrl}
                  size="mini"
                />
              </button>
            }
          >
            <Show when={shouldShowBubble()}>
              <button
                type="button"
                onClick={onAvatarActivate}
                class={`pointer-events-auto hoxi-companion-glass w-max max-w-[272px] rounded-2xl px-3.5 py-2.5 text-left text-[12px] leading-snug transition-all cursor-pointer ${
                  snap().mood === "gate-danger"
                    ? "border-[var(--danger-border)] text-[var(--danger-text)]"
                    : snap().mood === "gate-approval"
                      ? "border-[var(--warn-border)] text-[var(--text-primary)]"
                      : "text-[var(--text-secondary)]"
                }`}
              >
                <div class="mb-0.5 flex items-center justify-between gap-3">
                  <span class="font-semibold text-[11px] text-[var(--text-primary)]">
                    {resolveCompanionPersonaName(companionState.persona)}
                  </span>
                  <span class="font-mono text-[10.5px] opacity-80">
                    {badge().label}
                  </span>
                </div>
                <div class="whitespace-normal break-words">{dialogue()}</div>
              </button>
            </Show>

            <div
              class={`pointer-events-auto group/dock hoxi-companion-glass relative flex w-max max-w-[272px] items-center gap-2.5 rounded-2xl p-1.5 pr-3 ${
                snap().mood === "gate-danger" ? "border-[var(--danger-border)]" : ""
              }`}
            >
              <button
                type="button"
                onClick={onAvatarActivate}
                class="relative shrink-0 rounded-xl transition-transform active:scale-95 cursor-pointer"
                title={
                  isUrgentGate()
                    ? t("companion.actionJumpApproval")
                    : t("companion.actionPoke")
                }
              >
                <CompanionAvatar
                  persona={companionState.persona}
                  mood={snap().mood}
                  customUrl={companionState.customAvatarObjectUrl}
                  size="dock"
                />
              </button>

              <div class="min-w-0 pr-1">
                <div class="flex items-center gap-1.5 whitespace-nowrap">
                  <Show when={snap().mood === "gate-danger"}>
                    <ShieldAlert size={12} class="shrink-0 text-[var(--danger-text)] animate-pulse" />
                  </Show>
                  <span class="truncate text-[11.5px] font-semibold text-[var(--text-primary)]">
                    {badge().label}
                  </span>
                </div>
                <div class="font-mono text-[10.5px] text-[var(--text-muted)] whitespace-nowrap">
                  {t("companion.levelBadge", { level: snap().tokenLevel })}
                </div>
              </div>

              <button
                type="button"
                onClick={() => setCompanionMinimized(true)}
                title={t("companion.minimizeTooltip")}
                aria-label={t("companion.minimizeTooltip")}
                class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-md text-[var(--text-faint)] opacity-0 transition-opacity hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] group-hover/dock:opacity-100 cursor-pointer"
              >
                <Minimize2 size={11.5} />
              </button>
            </div>
          </Show>
        </div>
      </Show>
    </>
  );
}
