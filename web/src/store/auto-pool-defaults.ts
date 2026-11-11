// web/src/store/auto-pool-defaults.ts
import { createSignal } from "solid-js";
import { leaderboardAPI } from "../services/api";
import { workspace } from "./workspace-state";

export const AUTO_PROVIDER_ID = "hoxi-auto";

export const PRESET_AUTO_POOL_IDS = ["free", "daily", "frontier"] as const;
export type PresetAutoPoolID = (typeof PRESET_AUTO_POOL_IDS)[number];
export type AutoRotationStrategy = "round-robin" | "priority" | "smart-health";

export interface AutoPoolCandidate {
  providerID: string;
  modelID: string;
}

export interface AutoModelPoolConfig {
  id: string;
  name?: string;
  preset?: boolean;
  strategy: AutoRotationStrategy;
  customized: boolean;
  candidates: AutoPoolCandidate[];
}

export interface AutoCandidateCooldown {
  until: number;
  reason: string;
  statusCode?: number;
}

export interface FreeTierGuideItem {
  id: string;
  providerID: string;
  customMode?: boolean;
  baseURL?: string;
  defaultModels?: Array<{ id: string; name: string; reasoning: boolean; free: boolean }>;
  signupURL: string;
  title: string;
  badge?: string;
  quota: string;
  step: string;
  recommendedModels?: string[];
}

const [remoteFreeTierGuides, setRemoteFreeTierGuides] = createSignal<FreeTierGuideItem[]>([]);
let lastFreeTierFetchLang = "";

export function getFreeTierGuideItems(): readonly FreeTierGuideItem[] {
  return remoteFreeTierGuides();
}

export async function fetchFreeTierGuides(lang: string = "zh"): Promise<readonly FreeTierGuideItem[]> {
  const normalizedLang = lang.toLowerCase().startsWith("zh") ? "zh" : "en";
  if (remoteFreeTierGuides().length > 0 && lastFreeTierFetchLang === normalizedLang) {
    return remoteFreeTierGuides();
  }
  try {
    const items = await leaderboardAPI.getFreeTiers(normalizedLang);
    if (Array.isArray(items)) {
      const mapped: FreeTierGuideItem[] = items.map((item) => {
        const recModels = Array.isArray(item.recommendedModels) ? item.recommendedModels : [];
        return {
          id: item.id,
          providerID: item.providerID,
          customMode: Boolean(item.customMode),
          baseURL: item.baseUrl,
          signupURL: item.signupUrl,
          title: item.name,
          badge: item.badge,
          quota: item.quotaLabel,
          step: Array.isArray(item.steps) && item.steps.length > 0
            ? item.steps.map((s: string, idx: number) => `${idx + 1}. ${s}`).join(" · ")
            : "",
          recommendedModels: recModels,
          defaultModels: recModels.map((m: string) => ({
            id: m,
            name: m,
            reasoning: /r1|qwq|reason|thinking/i.test(m),
            free: true,
          })),
        };
      });
      lastFreeTierFetchLang = normalizedLang;
      setRemoteFreeTierGuides(mapped);
      return mapped;
    }
  } catch {
    /* ignore */
  }
  return remoteFreeTierGuides();
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (mInfo?.free === true || mInfo?.multiplier === "0x") return true;
  if (modelID.endsWith("-free") || modelID.endsWith(":free")) return true;
  if (
    mInfo?.cost &&
    mInfo.cost.input === 0 &&
    (mInfo.cost.output === 0 || mInfo.cost.output === undefined)
  ) {
    return true;
  }
  if (providerID !== "opencode" && providerID !== "opencode-go") return false;
  return !mInfo?.cost;
}

export function computeSmartDefaultCandidates(poolID: string): AutoPoolCandidate[] {
  const connSet = new Set(workspace.connectedProviders);
  const disSet = new Set<string>(
    Array.isArray(workspace.globalConfig?.disabled_providers)
      ? workspace.globalConfig.disabled_providers
      : []
  );
  const cfgProviders = workspace.globalConfig?.provider ?? {};

  const freeList: Array<{ cand: AutoPoolCandidate; score: number }> = [];
  const dailyList: Array<{ cand: AutoPoolCandidate; score: number }> = [];
  const frontierList: Array<{ cand: AutoPoolCandidate; score: number }> = [];

  for (const p of workspace.providers) {
    if (p.id === AUTO_PROVIDER_ID || disSet.has(p.id)) continue;
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]);
    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      if (isNonChatUtilityModelID(mID) || isImageGenerationModel(p.id, mID)) continue;
      const free = isModelFree(p.id, mID, mInfo);
      if (!isConn && !(p.id === "opencode" && free)) continue;
      const toolcall = mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true;
      if (!toolcall) continue;

      const lowerKey = `${p.id}/${mID}`.toLowerCase();
      const cand: AutoPoolCandidate = { providerID: p.id, modelID: mID };

      if (free) {
        let s = 50;
        if (/coder|claude|sonnet|gemini|deepseek|qwen|glm|kimi|minimax/i.test(lowerKey)) s += 40;
        if (p.source === "runner" || p.id !== "opencode") s += 20;
        freeList.push({ cand, score: s });
      }

      if (
        /deepseek|coder|sonnet|gemini.*flash|glm|gpt-.*(mini|nano|luna)|gpt-4|kimi|minimax|qwen.*(plus|coder|flash)/i.test(
          lowerKey
        ) ||
        free
      ) {
        let s = free ? 45 : 80;
        if (/sonnet|deepseek|qwen.*coder|gemini.*flash|glm/i.test(lowerKey)) s += 30;
        dailyList.push({ cand, score: s });
      }

      if (
        /opus|fable|sonnet|gpt-(?!.*(mini|nano|luna))|\/o\d+(-|\b)|gemini.*(pro|ultra|argon)|deepseek-(r\d+|reasoner|.*pro)|qwen.*max|qwq/i.test(
          lowerKey
        )
      ) {
        let s = 70;
        if (/opus|fable|gpt-(?!.*(mini|nano|luna))|gemini.*(pro|ultra|argon)|sonnet/i.test(lowerKey))
          s += 35;
        if (!free) s += 15;
        frontierList.push({ cand, score: s });
      }
    }
  }

  const pickSorted = (arr: Array<{ cand: AutoPoolCandidate; score: number }>, limit = 12) =>
    arr
      .sort((a, b) => b.score - a.score)
      .slice(0, limit)
      .map((x) => x.cand);

  if (poolID === "free") {
    const res = pickSorted(freeList, 14);
    return res.length > 0 ? res : pickSorted(dailyList, 6);
  }
  if (poolID === "frontier") {
    const res = pickSorted(frontierList, 10);
    return res.length > 0 ? res : pickSorted(dailyList, 8);
  }
  const res = pickSorted(dailyList, 12);
  return res.length > 0 ? res : pickSorted(freeList, 8);
}

const GEMINI_IMAGE_GEN_MODEL_RE =
  /nano-banana|gemini[\w./-]*(?:-image|_image|\/image|image-generation|image-preview)/i;
const MEDIA_IMAGE_GEN_MODEL_RE =
  /dall-e|gpt-image|chatgpt-image|gpt-4o-image|imagen|flux-|flux\.|flux\/|midjourney|stable-diffusion|sdxl|ideogram|recraft|cogview|seedream|kolors|hunyuan-image|qwen-image|wanx-v1/i;
const MULTIMODAL_IMAGE_EDIT_MODEL_RE =
  /nano-banana|gemini[\w./-]*(?:-image|_image|\/image|image-generation|image-preview)|gpt-image|chatgpt-image|gpt-4o-image|flux-kontext|seedream|qwen-image-edit/i;
const MEDIA_VIDEO_GEN_MODEL_RE =
  /seedance|kling|veo-|veo2|veo3|sora|wanx|wan2|cogvideo|hailuo|luma-|runway/i;
const MEDIA_AUDIO_GEN_MODEL_RE =
  /tts|speech|elevenlabs|fishaudio|cosyvoice|suno|udio/i;
const NON_CHAT_UTILITY_MODEL_RE =
  /embedding|embed|rerank|bge-|whisper|tts|speech|moderation/i;

export type DedicatedMediaModality = "image" | "video" | "audio" | null;

export function inferDedicatedMediaModality(modelID?: string | null): DedicatedMediaModality {
  if (!modelID) return null;
  const trimmed = modelID.trim();
  if (GEMINI_IMAGE_GEN_MODEL_RE.test(trimmed) || MEDIA_IMAGE_GEN_MODEL_RE.test(trimmed)) {
    return "image";
  }
  if (MEDIA_VIDEO_GEN_MODEL_RE.test(trimmed)) return "video";
  if (MEDIA_AUDIO_GEN_MODEL_RE.test(trimmed)) return "audio";
  return null;
}

export function supportsMultimodalImageEditModelID(modelID?: string | null): boolean {
  if (!modelID) return false;
  return MULTIMODAL_IMAGE_EDIT_MODEL_RE.test(modelID.trim());
}

export function isImageGenerationModel(
  providerID?: string | null,
  modelID?: string | null
): boolean {
  const mid = (modelID || "").trim();
  if (!mid) return false;
  if (inferDedicatedMediaModality(mid) === "image") return true;
  const pid = (providerID || "").trim();
  if (!pid) return false;
  const hoxiModel = workspace.hoxiProviders?.[pid]?.models?.[mid];
  if (hoxiModel && typeof hoxiModel === "object") {
    if (hoxiModel.category === "image") return true;
    if (Array.isArray(hoxiModel.modalities?.output) && hoxiModel.modalities.output.includes("image")) {
      return true;
    }
  }
  const prov = workspace.providers.find((p) => p.id === pid);
  const mInfo = prov?.models?.[mid] as Record<string, any> | undefined;
  if (mInfo && typeof mInfo === "object") {
    if (mInfo.category === "image") return true;
    if (Array.isArray(mInfo.modalities?.output) && mInfo.modalities.output.includes("image")) {
      return true;
    }
  }
  return false;
}

export function isNonChatUtilityModelID(modelID?: string | null): boolean {
  if (!modelID) return false;
  const trimmed = modelID.trim();
  if (!trimmed) return false;
  if (inferDedicatedMediaModality(trimmed) !== null) return true;
  return NON_CHAT_UTILITY_MODEL_RE.test(trimmed);
}

/**
 * Reports whether modelID is a Gemini 1.x or non-thinking Gemini 2.0 chat model
 * (e.g. gemini-1.5-pro, gemini-2.0-flash, gemini-2.0-flash-lite) that rejects
 * Google thinkingConfig payloads with HTTP 400.
 */
export function isNonThinkingGeminiModelID(modelID?: string | null): boolean {
  if (!modelID) return false;
  const clean = modelID.trim().replace(/^models\//i, "").toLowerCase();
  if (!clean.includes("gemini") || clean.includes("thinking")) return false;
  const sub = clean.slice(clean.indexOf("gemini"));
  const m = /^gemini[-_./: ]*(\d{1,2})(?:[-_./: ]+(\d{1,2}))?/.exec(sub);
  if (!m) return false;
  const major = Number(m[1]);
  const minor = m[2] !== undefined ? Number(m[2]) : 0;
  return major === 1 || (major === 2 && minor < 5);
}

export function inferProviderNpmFromEndpoint(
  providerID?: string | null,
  baseURL?: string | null
): string | null {
  const rawURL = (baseURL || "").trim();
  if (rawURL) {
    try {
      const parsed = new URL(rawURL);
      const host = parsed.hostname.toLowerCase();
      const path = parsed.pathname.replace(/\/+$/, "").toLowerCase();
      if (path.endsWith("/openai") || path.endsWith("/openai/v1") || path.includes("/openai/")) {
        return "@ai-sdk/openai-compatible";
      }
      if (
        host === "generativelanguage.googleapis.com" ||
        path.endsWith("/v1beta") ||
        path.endsWith("/v1alpha") ||
        path.endsWith("/v1beta/models") ||
        path.endsWith("/v1alpha/models")
      ) {
        return "@ai-sdk/google";
      }
      if (host === "api.anthropic.com") {
        return "@ai-sdk/anthropic";
      }
    } catch {
      /* ignore partial URL while typing */
    }
  }
  const pid = (providerID || "").trim().toLowerCase();
  if (pid === "google" || pid === "gemini" || pid.startsWith("gemini-") || pid.startsWith("google-")) {
    return "@ai-sdk/google";
  }
  if (pid === "anthropic" || pid.startsWith("anthropic-")) {
    return "@ai-sdk/anthropic";
  }
  return null;
}

export type PromptIntentKind =
  | "chat"
  | "image_gen"
  | "video_gen"
  | "visual_review"
  | "audio_tts"
  | "coding_reasoning";

export interface PromptIntentEvaluation {
  kind: PromptIntentKind;
  requiresVisionBrain: boolean;
  recommendedPoolID: PresetAutoPoolID;
  logicalMediaModelID: string | null;
  mediaModality: DedicatedMediaModality;
  defaultAspectRatio: "1:1" | "3:4" | "4:5" | "9:16" | "16:9";
  mediaRoutingDirective?: string;
}

const VIDEO_INTENT_RE =
  /storyboard-pipeline|15-ffmpeg-compose|\u751f\u89c6\u9891|\u751f\u6210\u89c6\u9891|\u77ed\u89c6\u9891\u811a\u672c|\u5206\u955c\u89c6\u9891|\u5e26\u8d27\u89c6\u9891|\u77ed\u5267|\u9996\u5c3e\u5e27|\u6302\u8f66\u89c6\u9891|seedance|kling|\u53ef\u7075|veo|sora|wanx|\u4e07\u76f8|\u5206\u955c\u72b6\u6001\u673a/i;
const VISUAL_REVIEW_INTENT_RE =
  /visual-brief-and-review|14-visual-review|\u89c6\u89c9\u590d\u76d8|\u81ea\u52a8\u590d\u76d8|\u5ba1\u56fe|\u68c0\u67e5\u4e3b\u56fe|\u5c01\u9762\u8bca\u65ad|\u5b89\u5168\u533a\u68c0\u67e5|\u906e\u6321\u68c0\u67e5|visual\s+review/i;
const IMAGE_INTENT_RE =
  /xhs-cover-3x4\.svg|hero-card\.svg|storyboard-9x16\.svg|\u751f\u56fe|\u751f\u6210\u56fe\u7247|\u751f\u6210\u4e3b\u56fe|\u5546\u54c1\u4e3b\u56fe|\u751f\u6210\u5c01\u9762|3:4\s*\u7ad6\u7248\u5c01\u9762|\u767d\u5e95\u56fe|\u573a\u666f\u56fe|\u5546\u62cd\u56fe|\u6d77\u62a5\u56fe|\u9996\u5c4f\u56fe\u5361|9\s*\u56fe\u811a\u672c|generate\s+image|product\s+photo/i;
const AUDIO_INTENT_RE =
  /\u914d\u97f3|\u53e3\u64ad\u97f3\u9891|\u8bed\u97f3\u5408\u6210|\u751f\u6210\u97f3\u9891|elevenlabs|fishaudio|tts|voiceover/i;
const XHS_VERTICAL_RE = /xiaohongshu|\u5c0f\u7ea2\u4e66|3:4|\u7ad6\u7248/i;

export function evaluatePromptModalityIntent(
  promptText: string,
  opts?: {
    hasImageAttachment?: boolean;
    skillName?: string;
    categoryId?: string;
    chipId?: string;
  }
): PromptIntentEvaluation {
  const raw = (promptText || "").trim();
  const combined = `${raw} ${opts?.skillName || ""} ${opts?.categoryId || ""} ${opts?.chipId || ""}`;

  if (VIDEO_INTENT_RE.test(combined)) {
    return {
      kind: "video_gen",
      requiresVisionBrain: true,
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-video-seedance",
      mediaModality: "video",
      defaultAspectRatio: "9:16",
      mediaRoutingDirective:
        "[Modality Router: Video & Storyboard Generation]\n- Keep the session orchestrator on a tool-calling Vision chat model.\n- Anchor subject & first-frame storageKeys via `storyboard-pipeline` (`15-storyboard-shots.csv`, `15-subtitles.srt`, `15-ffmpeg-compose.sh`) or invoke `hoxi_tools` (`bytedance` / `alibaba` video endpoints) when external video synthesis is requested.",
    };
  }

  if (VISUAL_REVIEW_INTENT_RE.test(combined)) {
    return {
      kind: "visual_review",
      requiresVisionBrain: true,
      recommendedPoolID: "frontier",
      logicalMediaModelID: "hoxi-image-pro",
      mediaModality: "image",
      defaultAspectRatio: "3:4",
      mediaRoutingDirective:
        "[Modality Router: Automated Visual Review]\n- Use Vision inspection capabilities and `visual-brief-and-review` to verify aspect ratios (1:1 / 3:4 / 9:16), UI safe-zone clearance, 1688 source image anchoring, contrast hierarchy, and ad compliance.",
    };
  }

  if (IMAGE_INTENT_RE.test(combined)) {
    const isXhsOrVertical = XHS_VERTICAL_RE.test(combined);
    const ratio = isXhsOrVertical ? "3:4" : "1:1";
    return {
      kind: "image_gen",
      requiresVisionBrain: true,
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-image-pro",
      mediaModality: "image",
      defaultAspectRatio: ratio,
      mediaRoutingDirective: `[Modality Router: Product Image & Cover Generation]\n- Do NOT treat a text-only chat response as the final visual deliverable.\n- Generate deterministic SVG/image assets (\`hero-card.svg\`, \`xhs-cover-3x4.svg\`, \`storyboard-9x16.svg\`) with stable workspace \`storageKey\` paths, or call \`hoxi_tools\` image generation endpoints when photorealistic rendering is requested.`,
    };
  }

  if (AUDIO_INTENT_RE.test(combined)) {
    return {
      kind: "audio_tts",
      requiresVisionBrain: Boolean(opts?.hasImageAttachment),
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-audio-tts",
      mediaModality: "audio",
      defaultAspectRatio: "9:16",
      mediaRoutingDirective:
        "[Modality Router: Voiceover & Subtitle Alignment]\n- Generate time-aligned `.srt` subtitles (`15-subtitles.srt`) and invoke `hoxi_tools` (`elevenlabs` / `fishaudio`) when audio synthesis is requested.",
    };
  }

  return {
    kind: opts?.hasImageAttachment ? "visual_review" : "chat",
    requiresVisionBrain: Boolean(opts?.hasImageAttachment),
    recommendedPoolID: "daily",
    logicalMediaModelID: null,
    mediaModality: null,
    defaultAspectRatio: "1:1",
  };
}

export function modelSupportsImageInput(providerID: string, modelID: string): boolean {
  if (!providerID || !modelID) return true;
  const hoxiModel = workspace.hoxiProviders?.[providerID]?.models?.[modelID];
  if (hoxiModel && typeof hoxiModel === "object") {
    if (hoxiModel.attachment === true) return true;
    if (Array.isArray(hoxiModel.modalities?.input) && hoxiModel.modalities.input.includes("image")) {
      return true;
    }
    if (hoxiModel.attachment === false) return false;
    if (Array.isArray(hoxiModel.modalities?.input) && !hoxiModel.modalities.input.includes("image")) {
      return false;
    }
  }
  const prov = workspace.providers.find((p) => p.id === providerID);
  const mInfo = prov?.models?.[modelID];
  if (mInfo && typeof mInfo === "object") {
    if (mInfo.attachment === true || mInfo.capabilities?.input?.image === true) return true;
    if (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) {
      return true;
    }
    if (mInfo.attachment === false) return false;
    if (
      mInfo.capabilities?.input?.image === false &&
      Array.isArray(mInfo.modalities?.input) &&
      !mInfo.modalities.input.includes("image")
    ) {
      return false;
    }
  }
  if (isImageGenerationModel(providerID, modelID)) {
    return supportsMultimodalImageEditModelID(modelID);
  }
  if (isNonChatUtilityModelID(modelID)) {
    return false;
  }
  return true;
}
