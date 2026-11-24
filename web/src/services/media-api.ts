// web/src/services/media-api.ts
import { requestJSON } from "./http";

export interface MediaImageModelOption {
  providerID: string;
  modelID: string;
  name?: string;
  protocol?: string;
  supportsImageInput?: boolean;
  capabilities?: string[];
}

export interface MediaSlotModelOption {
  providerID: string;
  modelID: string;
  name?: string;
  modality?: string;
  capabilities?: string[];
  image_translate?: { output_modes?: string[] };
  supportsImageInput?: boolean;
}

export interface MediaVideoModelOption {
  providerID: string;
  modelID: string;
  name?: string;
  supportsImageInput?: boolean;
}

export interface MediaAudioModelOption {
  providerID: string;
  modelID: string;
  name?: string;
}

export interface MediaRolePrefs {
  imageModel?: string;
  imageEditModel?: string;
  imageTranslateRasterModel?: string;
  imageTranslateEditorModel?: string;
  videoModel?: string;
  speechModel?: string;
  transcribeModel?: string;
  visionModel?: string;
  textTranslateModel?: string;
  embeddingModel?: string;
  rerankModel?: string;
}

export interface MediaCapabilitiesResponse {
  ok: boolean;
  hasImageModel: boolean;
  hasImageEditModel?: boolean;
  hasImageTranslateRasterModel?: boolean;
  hasImageTranslateEditorModel?: boolean;
  hasVideoModel?: boolean;
  hasSpeechModel?: boolean;
  hasAudioTranscriber: boolean;
  hasEmbeddingModel: boolean;
  imageModels?: MediaImageModelOption[];
  imageEditModels?: MediaSlotModelOption[];
  imageTranslateRasterModels?: MediaSlotModelOption[];
  imageTranslateEditorModels?: MediaSlotModelOption[];
  videoModels?: MediaVideoModelOption[];
  speechModels?: MediaAudioModelOption[];
  transcribeModels?: MediaAudioModelOption[];
  slots?: Record<string, MediaSlotModelOption[]>;
  roles?: MediaRolePrefs;
}

export interface MediaImageTranslateInput {
  requestId?: string;
  image: string;
  sourceLanguage?: string;
  targetLanguage?: string;
  providerID?: string;
  model?: string;
  modelID?: string;
  outputMode?: "raster" | "editable_scene";
}

export interface MediaImageTranslateResponse {
  ok: boolean;
  requestId?: string;
  providerID?: string;
  modelID?: string;
  resolution?: string;
  finalImageUrl?: string;
  inPaintingUrl?: string;
  scene?: unknown;
  missingCapability?: string;
  error?: string;
}

export interface MediaTranscribeResponse {
  ok: boolean;
  text?: string;
  providerID?: string;
  modelID?: string;
  error?: string;
}

export interface MediaGenerateImageInput {
  prompt: string;
  providerID?: string;
  modelID?: string;
  aspectRatio?: string;
  imageSize?: string;
  count?: number;
  sessionID?: string;
  directory?: string;
  inputPaths?: string[];
  outputPath?: string;
}

export interface MediaGenerateImageResponse {
  ok: boolean;
  providerID?: string;
  modelID?: string;
  resolution?: string;
  fallbackTool?: string;
  text?: string;
  images?: Array<{
    filename: string;
    mime: string;
    url?: string;
    path?: string;
  }>;
  error?: string;
}

export interface MediaGenerateVideoInput {
  prompt: string;
  providerID?: string;
  modelID?: string;
  aspectRatio?: string;
  resolution?: string;
  durationSeconds?: number;
  sessionID?: string;
  directory?: string;
  inputPaths?: string[];
  outputPath?: string;
}

export interface MediaGenerateVideoResponse {
  ok: boolean;
  providerID?: string;
  modelID?: string;
  resolution?: string;
  fallbackTool?: string;
  text?: string;
  videos?: Array<{
    filename: string;
    mime: string;
    url?: string;
    path?: string;
  }>;
  error?: string;
}

export const mediaAPI = {
  getMediaCapabilities(): Promise<MediaCapabilitiesResponse> {
    return requestJSON("/api/hoxi/media/capabilities");
  },

  transcribeAudio(input: {
    dataUrl: string;
    filename?: string;
    mime?: string;
    language?: string;
    prompt?: string;
    providerID?: string;
    modelID?: string;
  }): Promise<MediaTranscribeResponse> {
    return requestJSON("/api/hoxi/media/transcribe", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  generateImageMedia(
    input: MediaGenerateImageInput
  ): Promise<MediaGenerateImageResponse> {
    return requestJSON("/api/hoxi/media/image", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  translateImageMedia(
    input: MediaImageTranslateInput
  ): Promise<MediaImageTranslateResponse> {
    return requestJSON("/api/hoxi/media/image-translate", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  generateVideoMedia(
    input: MediaGenerateVideoInput
  ): Promise<MediaGenerateVideoResponse> {
    return requestJSON("/api/hoxi/media/video", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  getMediaRoles(): Promise<{ ok: boolean; roles: MediaRolePrefs }> {
    return requestJSON("/api/hoxi/media/roles");
  },

  updateMediaRoles(
    patch: Partial<Record<keyof MediaRolePrefs, string | null>>
  ): Promise<{ ok: boolean; roles: MediaRolePrefs }> {
    return requestJSON("/api/hoxi/media/roles", {
      method: "PUT",
      body: JSON.stringify(patch),
    });
  },
};
