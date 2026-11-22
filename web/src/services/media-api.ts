// web/src/services/media-api.ts
import { requestJSON } from "./http";

export interface MediaImageModelOption {
  providerID: string;
  modelID: string;
  name?: string;
  protocol?: string;
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
  videoModel?: string;
  speechModel?: string;
  transcribeModel?: string;
}

export interface MediaCapabilitiesResponse {
  ok: boolean;
  hasImageModel: boolean;
  hasVideoModel?: boolean;
  hasSpeechModel?: boolean;
  hasAudioTranscriber: boolean;
  hasEmbeddingModel: boolean;
  imageModels?: MediaImageModelOption[];
  videoModels?: MediaVideoModelOption[];
  speechModels?: MediaAudioModelOption[];
  transcribeModels?: MediaAudioModelOption[];
  roles?: MediaRolePrefs;
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
