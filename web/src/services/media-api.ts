// web/src/services/media-api.ts
import type {
  MediaCapabilitiesResponse,
  MediaTranscribeResponse,
} from "./api-types";
import { requestJSON } from "./http";

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
  text?: string;
  images?: Array<{
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
};
