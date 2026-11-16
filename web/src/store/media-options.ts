// web/src/store/media-options.ts
import { createSignal } from "solid-js";
import {
  hoxiAPI,
  type MediaCapabilitiesResponse,
  type PromptImageOptions,
} from "../services/api";
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
    hasAudioTranscriber: false,
    hasEmbeddingModel: false,
    imageModels: [],
  });

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
