import { tool } from "./plugin_tool.js";
import { isProtectedPath, isSecretPath, isSensitiveExternalPath } from "../../web/src/policy/permission-policy.js";

function isDisallowedMediaPath(rawPath) {
  if (typeof rawPath !== "string") return false;
  const trimmed = rawPath.trim();
  if (!trimmed) return false;
  const parts = trimmed.split(/[\\/]+/).filter(Boolean);
  if (parts.includes("..")) return true;
  return isProtectedPath(trimmed) || isSecretPath(trimmed) || isSensitiveExternalPath(trimmed);
}

export function createImageGenerationTool() {
  const imageArgs = {
    prompt: tool.schema
      .string()
      .min(1)
      .max(4000)
      .describe("Detailed visual prompt describing the image to generate or how to edit the reference image(s)"),
    outputPath: tool.schema
      .string()
      .optional()
      .describe("Optional workspace-relative file path to save the generated image (e.g. 'public/hero.png'). If omitted, saved under .hoxi/attachments/"),
    inputPaths: tool.schema
      .array(tool.schema.string())
      .optional()
      .describe("Optional workspace-relative image paths to use as reference images for image-to-image editing"),
    aspectRatio: tool.schema
      .enum(["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"])
      .optional()
      .describe("Target aspect ratio (default '1:1')"),
    imageSize: tool.schema
      .enum(["1K", "2K", "4K"])
      .optional()
      .describe("Optional output resolution tier when supported by the provider (e.g. Gemini 3 Pro Image)"),
    providerID: tool.schema
      .string()
      .optional()
      .describe("Optional configured image provider ID; auto-selects the best configured local image model when omitted"),
    modelID: tool.schema
      .string()
      .optional()
      .describe("Optional configured image model ID; auto-selects when omitted"),
  };
  const imageSchema = tool.schema.object(imageArgs);

  return tool({
    description:
      "Generate or edit images directly using the user's configured local/custom image models (Gemini Image / Nano-Banana, Imagen, GPT-Image, Flux, Seedream) without switching the active coding/chat model. Saves the generated image into the workspace or .hoxi/attachments/ and returns its path and attachment URL.",
    args: imageArgs,
    async execute(input, context) {
      const req = imageSchema.parse(input);
      if (!context.directory) throw new Error("Workspace directory scope is required for hoxi_image");
      const rawBase = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
      if (!rawBase) throw new Error("Hoxi gateway URL is unavailable");
      context.abort.throwIfAborted();

      if (req.outputPath && isDisallowedMediaPath(req.outputPath)) {
        throw new Error(`Refusing to write image to protected or outside-workspace path: ${req.outputPath}`);
      }
      if (Array.isArray(req.inputPaths)) {
        for (const p of req.inputPaths) {
          if (isDisallowedMediaPath(p)) {
            throw new Error(`Refusing to read reference image from protected or secret path: ${p}`);
          }
        }
      }

      if (req.outputPath && typeof context.ask === "function") {
        await context.ask({
          permission: "edit",
          patterns: [req.outputPath],
          always: [req.outputPath],
          metadata: {
            tool: "hoxi_image",
            filepath: req.outputPath,
            prompt: req.prompt,
            sessionID: context.sessionID,
            directory: context.directory,
          },
        });
      }
      context.abort.throwIfAborted();

      const base = rawBase.replace(/\/$/, "");
      const url = new URL(`${base}/api/hoxi/media/image`);
      url.searchParams.set("directory", context.directory);
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt: req.prompt,
          outputPath: req.outputPath,
          inputPaths: req.inputPaths,
          aspectRatio: req.aspectRatio,
          imageSize: req.imageSize,
          providerID: req.providerID,
          modelID: req.modelID,
          sessionID: context.sessionID,
          directory: context.directory,
        }),
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) {
        throw new Error(`Hoxi image generation HTTP ${response.status}: ${await response.text()}`);
      }
      const data = await response.json();
      if (!data?.ok) {
        throw new Error(data?.error || "Image generation failed");
      }
      const lines = [
        `Generated ${Array.isArray(data.images) ? data.images.length : 1} image(s) via ${data.providerID}/${data.modelID}.`,
      ];
      if (data.text) lines.push(`Model note: ${data.text}`);
      const attachments = [];
      for (const item of data.images || []) {
        lines.push(`- File: ${item.path || item.filename} (attachment URL: ${item.url})`);
        if (item.url) {
          attachments.push({
            type: "file",
            mime: item.mime || "image/png",
            filename: item.filename || "generated.png",
            url: item.url,
          });
        }
      }
      return {
        title: `HoxiImage: ${data.modelID || "generated"}`,
        output: lines.join("\n"),
        ...(attachments.length > 0 ? { attachments } : {}),
      };
    },
  });
}

export function createVideoGenerationTool() {
  const videoArgs = {
    prompt: tool.schema
      .string()
      .min(1)
      .max(4000)
      .describe("Detailed video motion, scene, and camera prompt"),
    outputPath: tool.schema
      .string()
      .optional()
      .describe("Optional workspace-relative file path to save the generated video (e.g. 'public/intro.mp4'). If omitted, saved under .hoxi/attachments/"),
    inputPaths: tool.schema
      .array(tool.schema.string())
      .optional()
      .describe("Optional workspace-relative first-frame reference image path(s) for image-to-video"),
    aspectRatio: tool.schema
      .enum(["16:9", "9:16", "1:1", "4:3", "3:4"])
      .optional()
      .describe("Target aspect ratio (default '16:9')"),
    resolution: tool.schema
      .enum(["720p", "1080p"])
      .optional()
      .describe("Optional output resolution tier"),
    durationSeconds: tool.schema
      .number()
      .int()
      .min(2)
      .max(20)
      .optional()
      .describe("Optional clip duration in seconds (2-20)"),
    providerID: tool.schema
      .string()
      .optional()
      .describe("Optional configured video provider ID; checks active model first, then configured local video models"),
    modelID: tool.schema
      .string()
      .optional()
      .describe("Optional configured video model ID; auto-selects when omitted"),
  };
  const videoSchema = tool.schema.object(videoArgs);

  return tool({
    description:
      "Generate text-to-video or image-to-video clips: checks the active session model's native video capability first, then configured local video models (Veo, Sora, Wan, Seedance, Kling, CogVideo), and saves the video to disk (fall back to hoxi_tools only if unavailable).",
    args: videoArgs,
    async execute(input, context) {
      const req = videoSchema.parse(input);
      if (!context.directory) throw new Error("Workspace directory scope is required for hoxi_video");
      const rawBase = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
      if (!rawBase) throw new Error("Hoxi gateway URL is unavailable");
      context.abort.throwIfAborted();

      if (req.outputPath && isDisallowedMediaPath(req.outputPath)) {
        throw new Error(`Refusing to write video to protected or outside-workspace path: ${req.outputPath}`);
      }
      if (Array.isArray(req.inputPaths)) {
        for (const p of req.inputPaths) {
          if (isDisallowedMediaPath(p)) {
            throw new Error(`Refusing to read reference image from protected or secret path: ${p}`);
          }
        }
      }

      if (req.outputPath && typeof context.ask === "function") {
        await context.ask({
          permission: "edit",
          patterns: [req.outputPath],
          always: [req.outputPath],
          metadata: {
            tool: "hoxi_video",
            filepath: req.outputPath,
            prompt: req.prompt,
            sessionID: context.sessionID,
            directory: context.directory,
          },
        });
      }
      context.abort.throwIfAborted();

      const base = rawBase.replace(/\/$/, "");
      const url = new URL(`${base}/api/hoxi/media/video`);
      url.searchParams.set("directory", context.directory);
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt: req.prompt,
          outputPath: req.outputPath,
          inputPaths: req.inputPaths,
          aspectRatio: req.aspectRatio,
          resolution: req.resolution,
          durationSeconds: req.durationSeconds,
          providerID: req.providerID,
          modelID: req.modelID,
          sessionID: context.sessionID,
          directory: context.directory,
        }),
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) {
        throw new Error(`Hoxi video generation HTTP ${response.status}: ${await response.text()}`);
      }
      const data = await response.json();
      if (!data?.ok) {
        throw new Error(data?.error || "Video generation failed");
      }
      const lines = [
        `Generated ${Array.isArray(data.videos) ? data.videos.length : 1} video(s) via ${data.providerID}/${data.modelID}.`,
      ];
      if (data.text) lines.push(`Model note: ${data.text}`);
      const attachments = [];
      for (const item of data.videos || []) {
        lines.push(`- File: ${item.path || item.filename} (attachment URL: ${item.url})`);
        if (item.url) {
          attachments.push({
            type: "file",
            mime: item.mime || "video/mp4",
            filename: item.filename || "generated.mp4",
            url: item.url,
          });
        }
      }
      return {
        title: `HoxiVideo: ${data.modelID || "generated"}`,
        output: lines.join("\n"),
        ...(attachments.length > 0 ? { attachments } : {}),
      };
    },
  });
}

export function createSpeechSynthesisTool() {
  const speechArgs = {
    text: tool.schema
      .string()
      .min(1)
      .max(8000)
      .describe("Spoken script or text to synthesize into audio"),
    outputPath: tool.schema
      .string()
      .optional()
      .describe("Optional workspace-relative file path to save the synthesized audio (e.g. 'public/voiceover.mp3')"),
    voice: tool.schema
      .string()
      .optional()
      .describe("Optional voice name (e.g. 'alloy', 'nova', 'Kore')"),
    format: tool.schema
      .enum(["mp3", "wav", "opus", "flac", "aac"])
      .optional()
      .describe("Optional output audio format (default 'mp3')"),
    speed: tool.schema
      .number()
      .min(0.25)
      .max(4)
      .optional()
      .describe("Optional playback speed (0.25 - 4.0)"),
    providerID: tool.schema
      .string()
      .optional()
      .describe("Optional configured speech provider ID"),
    modelID: tool.schema
      .string()
      .optional()
      .describe("Optional configured speech model ID"),
  };
  const speechSchema = tool.schema.object(speechArgs);

  return tool({
    description:
      "Synthesize spoken audio (TTS / voiceover) from text: checks the active session model's native audio capability first, then configured local TTS models, and saves the audio to disk (fall back to hoxi_tools only if unavailable).",
    args: speechArgs,
    async execute(input, context) {
      const req = speechSchema.parse(input);
      if (!context.directory) throw new Error("Workspace directory scope is required for hoxi_speech");
      const rawBase = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
      if (!rawBase) throw new Error("Hoxi gateway URL is unavailable");
      context.abort.throwIfAborted();

      if (req.outputPath && isDisallowedMediaPath(req.outputPath)) {
        throw new Error(`Refusing to write audio to protected or outside-workspace path: ${req.outputPath}`);
      }

      if (req.outputPath && typeof context.ask === "function") {
        await context.ask({
          permission: "edit",
          patterns: [req.outputPath],
          always: [req.outputPath],
          metadata: {
            tool: "hoxi_speech",
            filepath: req.outputPath,
            text: req.text,
            sessionID: context.sessionID,
            directory: context.directory,
          },
        });
      }
      context.abort.throwIfAborted();

      const base = rawBase.replace(/\/$/, "");
      const url = new URL(`${base}/api/hoxi/media/speech`);
      url.searchParams.set("directory", context.directory);
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          text: req.text,
          outputPath: req.outputPath,
          voice: req.voice,
          format: req.format,
          speed: req.speed,
          providerID: req.providerID,
          modelID: req.modelID,
          sessionID: context.sessionID,
          directory: context.directory,
        }),
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) {
        throw new Error(`Hoxi speech synthesis HTTP ${response.status}: ${await response.text()}`);
      }
      const data = await response.json();
      if (!data?.ok) {
        throw new Error(data?.error || "Speech synthesis failed");
      }
      const lines = [`Synthesized speech via ${data.providerID}/${data.modelID}.`];
      const attachments = [];
      for (const item of data.audios || []) {
        lines.push(`- File: ${item.path || item.filename} (attachment URL: ${item.url})`);
        if (item.url) {
          attachments.push({
            type: "file",
            mime: item.mime || "audio/mpeg",
            filename: item.filename || "generated.mp3",
            url: item.url,
          });
        }
      }
      return {
        title: `HoxiSpeech: ${data.modelID || "generated"}`,
        output: lines.join("\n"),
        ...(attachments.length > 0 ? { attachments } : {}),
      };
    },
  });
}
