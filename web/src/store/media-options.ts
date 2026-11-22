// web/src/store/media-options.ts
import { createSignal } from "solid-js";
import {
  hoxiAPI,
  type MediaCapabilitiesResponse,
  type MediaRolePrefs,
  type PromptImageOptions,
} from "../services/api";
import {
  AUTO_PROVIDER_ID,
  isImageGenerationModel,
  isSpeechSynthesisModel,
  isVideoGenerationModel,
} from "./auto-pool-defaults";
import type { ConnectedProviderGroup } from "./provider-catalog";
import { workspace } from "./workspace-state";

export const IMAGE_ASPECT_RATIOS = ["1:1", "16:9", "9:16", "4:3", "3:4"] as const;
export type ImageAspectRatio = (typeof IMAGE_ASPECT_RATIOS)[number];

export const IMAGE_SIZE_TIERS = ["1K", "2K", "4K"] as const;
export type ImageSizeTier = (typeof IMAGE_SIZE_TIERS)[number];

const [selectedImageAspectRatio, setSelectedImageAspectRatio] =
  createSignal<ImageAspectRatio>("1:1");
const [selectedImageSize, setSelectedImageSize] =
  createSignal<ImageSizeTier>("1K");

const [mediaCapabilities, setMediaCapabilities] =
  createSignal<MediaCapabilitiesResponse>({
    ok: true,
    hasImageModel: false,
    hasVideoModel: false,
    hasSpeechModel: false,
    hasAudioTranscriber: false,
    hasEmbeddingModel: false,
    imageModels: [],
    videoModels: [],
    speechModels: [],
    transcribeModels: [],
    roles: {},
  });

export type MediaRoleKind = "image" | "video" | "speech" | "transcribe";

export function mediaRolePrefs(): MediaRolePrefs {
  return mediaCapabilities().roles || {};
}

export async function updateMediaRolePrefs(
  patch: Partial<Record<keyof MediaRolePrefs, string | null>>
): Promise<MediaRolePrefs> {
  const res = await hoxiAPI.updateMediaRoles(patch);
  const nextRoles = res?.roles || {};
  setMediaCapabilities((prev) => ({
    ...prev,
    roles: nextRoles,
  }));
  return nextRoles;
}

export function getMediaRoleModelGroups(kind: MediaRoleKind): ConnectedProviderGroup[] {
  const caps = mediaCapabilities();
  const byProv = new Map<string, { name: string; models: Map<string, string> }>();
  const provNameLookup = new Map<string, string>();
  for (const p of workspace.providers) {
    if (p?.id) provNameLookup.set(p.id, p.name || p.id);
  }
  for (const [pid, pVal] of Object.entries(workspace.hoxiProviders || {})) {
    if (pid && !provNameLookup.has(pid)) {
      provNameLookup.set(pid, (pVal as any)?.name || pid);
    }
  }

  const addEntry = (pid?: string, mid?: string, name?: string) => {
    const p = (pid || "").trim();
    const m = (mid || "").trim();
    if (!p || !m || p === AUTO_PROVIDER_ID) return;
    let group = byProv.get(p);
    if (!group) {
      group = { name: provNameLookup.get(p) || p, models: new Map() };
      byProv.set(p, group);
    }
    if (!group.models.has(m)) {
      group.models.set(m, (name || "").trim() || m);
    }
  };

  const serverList =
    kind === "image"
      ? caps.imageModels
      : kind === "video"
      ? caps.videoModels
      : kind === "speech"
      ? caps.speechModels
      : caps.transcribeModels;
  for (const item of serverList || []) {
    addEntry(item.providerID, item.modelID, item.name);
  }

  for (const prov of workspace.providers) {
    if (!prov || prov.id === AUTO_PROVIDER_ID) continue;
    for (const [mid, mInfo] of Object.entries(prov.models || {})) {
      const id = mid.toLowerCase();
      const match =
        kind === "image"
          ? isImageGenerationModel(prov.id, mid)
          : kind === "video"
          ? isVideoGenerationModel(prov.id, mid)
          : kind === "speech"
          ? isSpeechSynthesisModel(prov.id, mid)
          : id.includes("whisper") ||
            id.includes("sensevoice") ||
            id.includes("paraformer") ||
            id.includes("transcribe") ||
            (id.includes("gemini") &&
              !id.includes("image") &&
              !id.includes("embed") &&
              Boolean(mInfo?.capabilities?.input?.audio || mInfo?.modalities?.input?.includes("audio")));
      if (match) addEntry(prov.id, mid, mInfo?.name);
    }
  }

  return Array.from(byProv.entries()).map(([providerID, g]) => ({
    providerID,
    providerName: g.name,
    models: Array.from(g.models.entries()).map(([id, name]) => ({
      id,
      name,
      fullKey: `${providerID}/${id}`,
      free: false,
    })),
  }));
}

export {
  mediaCapabilities,
  selectedImageAspectRatio,
  selectedImageSize,
  setSelectedImageAspectRatio,
  setSelectedImageSize,
};

export function currentPromptImageOptions(): PromptImageOptions {
  return {
    aspectRatio: selectedImageAspectRatio(),
    imageSize: selectedImageSize(),
    count: 1,
  };
}

let refreshInFlight: Promise<MediaCapabilitiesResponse> | null = null;

export async function refreshMediaCapabilities(): Promise<MediaCapabilitiesResponse> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = hoxiAPI
    .getMediaCapabilities()
    .then((res) => {
      if (res && typeof res === "object") {
        setMediaCapabilities(res);
      }
      return mediaCapabilities();
    })
    .catch(() => mediaCapabilities())
    .finally(() => {
      refreshInFlight = null;
    });
  return refreshInFlight;
}

export function hasConfiguredAudioTranscriber(): boolean {
  if (mediaCapabilities().hasAudioTranscriber) return true;
  for (const prov of workspace.providers) {
    if (prov.id === "hoxi-official") continue;
    for (const model of Object.values(prov.models || {})) {
      const id = (model.id || "").toLowerCase();
      if (
        id.includes("whisper") ||
        id.includes("sensevoice") ||
        id.includes("paraformer") ||
        id.includes("transcribe") ||
        (id.includes("gemini") &&
          !id.includes("image") &&
          !id.includes("embed") &&
          Boolean(model.capabilities?.input?.audio || model.modalities?.input?.includes("audio")))
      ) {
        return true;
      }
    }
  }
  return false;
}

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("failed to read recorded audio"));
    reader.onload = () => {
      if (typeof reader.result === "string") resolve(reader.result);
      else reject(new Error("empty audio buffer"));
    };
    reader.readAsDataURL(blob);
  });
}

function audioFilenameForMime(mime: string): string {
  const lower = (mime || "").toLowerCase();
  if (lower.includes("mp4") || lower.includes("m4a")) return "voice.m4a";
  if (lower.includes("ogg")) return "voice.ogg";
  if (lower.includes("wav")) return "voice.wav";
  if (lower.includes("mpeg") || lower.includes("mp3")) return "voice.mp3";
  return "voice.webm";
}

export async function transcribeComposerAudio(
  blob: Blob,
  localeHint?: string
): Promise<string> {
  const mime = (blob.type || "audio/webm").split(";")[0].trim() || "audio/webm";
  const dataUrl = await blobToDataUrl(blob);
  const lang = (localeHint || "").toLowerCase().startsWith("zh")
    ? "zh"
    : (localeHint || "").split("-")[0] || undefined;
  const res = await hoxiAPI.transcribeAudio({
    dataUrl,
    filename: audioFilenameForMime(mime),
    mime,
    language: lang,
  });
  if (!res?.ok) {
    throw new Error(res?.error || "transcription failed");
  }
  return (res.text || "").trim();
}
