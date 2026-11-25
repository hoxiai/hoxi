// web/src/store/share-image.ts
// Global state and remembered preferences for the local Session Share Image modal.
import { createSignal } from "solid-js";
import type { SessionInfo } from "../services/sse-types";

export type ShareImageThemeOption = "light" | "dark" | "system";
export type ShareImageWidthOption = "standard" | "wide";
export type ShareImageLayoutOption = "single" | "paged";

export interface ShareImagePreferences {
  theme: ShareImageThemeOption;
  width: ShareImageWidthOption;
  layout: ShareImageLayoutOption;
  includeTools: boolean;
  includeThinking: boolean;
  includeImages: boolean;
  hideLocalPaths: boolean;
  redactSecrets: boolean;
  showBrandFooter: boolean;
}

export interface ShareImageModalRequest {
  sessionID: string;
  /** When opened from an assistant turn footer, pre-select only that Q&A exchange. */
  focusMessageID?: string;
  /** Optional snapshot of session metadata (covers cross-project sidebar items). */
  session?: SessionInfo;
}

const PREFS_STORAGE_KEY = "hoxi.share-image-prefs:v1";

export const DEFAULT_SHARE_IMAGE_PREFS: ShareImagePreferences = {
  theme: "light",
  width: "standard",
  layout: "single",
  includeTools: true,
  includeThinking: false,
  includeImages: true,
  hideLocalPaths: true,
  redactSecrets: true,
  showBrandFooter: true,
};

function loadPreferences(): ShareImagePreferences {
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(PREFS_STORAGE_KEY) : null;
    if (!raw) return { ...DEFAULT_SHARE_IMAGE_PREFS };
    const parsed = JSON.parse(raw) as Partial<ShareImagePreferences> | null;
    if (!parsed || typeof parsed !== "object") return { ...DEFAULT_SHARE_IMAGE_PREFS };
    return {
      theme: parsed.theme === "dark" || parsed.theme === "system" ? parsed.theme : "light",
      width: parsed.width === "wide" ? "wide" : "standard",
      layout: parsed.layout === "paged" ? "paged" : "single",
      includeTools: typeof parsed.includeTools === "boolean" ? parsed.includeTools : true,
      includeThinking: typeof parsed.includeThinking === "boolean" ? parsed.includeThinking : false,
      includeImages: typeof parsed.includeImages === "boolean" ? parsed.includeImages : true,
      hideLocalPaths: typeof parsed.hideLocalPaths === "boolean" ? parsed.hideLocalPaths : true,
      redactSecrets: typeof parsed.redactSecrets === "boolean" ? parsed.redactSecrets : true,
      showBrandFooter: typeof parsed.showBrandFooter === "boolean" ? parsed.showBrandFooter : true,
    };
  } catch {
    return { ...DEFAULT_SHARE_IMAGE_PREFS };
  }
}

const [modalRequest, setModalRequest] = createSignal<ShareImageModalRequest | null>(null);
const [prefs, setPrefs] = createSignal<ShareImagePreferences>(loadPreferences());

export const shareImageModal = modalRequest;
export const shareImageModalOpen = () => modalRequest() !== null;
export const shareImagePrefs = prefs;

export function openShareImageModal(request: ShareImageModalRequest): void {
  const sessionID = (request.sessionID || "").trim();
  if (!sessionID) return;
  const focusMessageID = request.focusMessageID?.trim() || undefined;
  setModalRequest({ sessionID, focusMessageID, session: request.session });
}

export function closeShareImageModal(): void {
  setModalRequest(null);
}

export function updateShareImagePrefs(patch: Partial<ShareImagePreferences>): void {
  setPrefs((current) => {
    const next: ShareImagePreferences = { ...current, ...patch };
    try {
      if (typeof localStorage !== "undefined") {
        localStorage.setItem(PREFS_STORAGE_KEY, JSON.stringify(next));
      }
    } catch {
      /* ignore storage errors */
    }
    return next;
  });
}
