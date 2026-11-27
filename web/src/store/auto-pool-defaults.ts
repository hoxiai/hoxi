// web/src/store/auto-pool-defaults.ts
import { createSignal } from "solid-js";
import { parseHoxiContextPrompt } from "./context-protocol";
import { leaderboardAPI } from "../services/api";
import { preferredAgentEngine, workspace } from "./workspace-state";

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
  if (mInfo?.free === false) return false;
  if (mInfo?.free === true || mInfo?.multiplier === "0x") return true;
  if (modelID.endsWith("-free") || modelID.endsWith(":free")) return true;
  // Zero costs from imported CLI/subscription models are placeholders, not
  // evidence of a free tier. Only built-in OpenCode catalogs use this fallback.
  if (providerID !== "opencode" && providerID !== "opencode-go") return false;
  if (
    mInfo?.cost &&
    mInfo.cost.input === 0 &&
    (mInfo.cost.output === 0 || mInfo.cost.output === undefined)
  ) {
    return true;
  }
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
  const openCodeAvailable =
    connSet.has("opencode") ||
    Boolean(cfgProviders.opencode) ||
    preferredAgentEngine() === "opencode";

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
      if (!isConn && !(openCodeAvailable && p.id === "opencode" && free)) continue;
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
        /opus|fable|sonnet|gpt-(?!.*(mini|nano|luna))|\/o\d+(-|\b)|gemini.*(pro|ultra|argon|carbon)|deepseek-(r\d+|reasoner|.*pro)|qwen.*max|qwq/i.test(
          lowerKey
        )
      ) {
        let s = 70;
        if (
          /opus|fable|gpt-(?!.*(mini|nano|luna))|gemini.*(pro|ultra|argon|carbon)|sonnet/i.test(
            lowerKey
          )
        )
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
    return pickSorted(freeList, 14);
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
  /nano-banana|gemini[\w./-]*(?:-image|_image|\/image|image-generation|image-preview)|gpt-image|dall-e-2|imagen|qwen-image|kontext|inpaint|flux-fill|seedream|-edit|chatgpt-image|gpt-4o-image/i;
const MEDIA_VIDEO_GEN_MODEL_RE =
  /seedance|kling|veo-|veo2|veo3|sora|wanx|wan2|cogvideo|hailuo|luma-|runway|vidu/i;
const MEDIA_AUDIO_GEN_MODEL_RE =
  /tts|speech|elevenlabs|fishaudio|cosyvoice|suno|udio/i;
const NON_CHAT_UTILITY_MODEL_RE =
  /embedding|embed|rerank|bge-|whisper|tts|speech|moderation|alimt-image-translate|image-translate|mt-image|collector-|codex-auto-review/i;

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
  const trimmed = modelID.trim();
  const lower = trimmed.toLowerCase();
  if (lower.includes("imagen") && lower.includes("-generate") && !lower.includes("edit") && !lower.includes("inpaint")) {
    return false;
  }
  return MULTIMODAL_IMAGE_EDIT_MODEL_RE.test(trimmed);
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
  const hoxiModel =
    workspace.hoxiProviders?.[pid]?.models?.[mid] ??
    workspace.hoxiProviders?.[pid]?.mediaModels?.[mid];
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

export function isVideoGenerationModel(
  providerID?: string | null,
  modelID?: string | null
): boolean {
  const mid = (modelID || "").trim();
  if (!mid) return false;
  if (inferDedicatedMediaModality(mid) === "video") return true;
  const pid = (providerID || "").trim();
  if (!pid) return false;
  const hoxiModel =
    workspace.hoxiProviders?.[pid]?.models?.[mid] ??
    workspace.hoxiProviders?.[pid]?.mediaModels?.[mid];
  if (hoxiModel && typeof hoxiModel === "object") {
    if (hoxiModel.category === "video") return true;
    if (Array.isArray(hoxiModel.modalities?.output) && hoxiModel.modalities.output.includes("video")) {
      return true;
    }
  }
  const prov = workspace.providers.find((p) => p.id === pid);
  const mInfo = prov?.models?.[mid] as Record<string, any> | undefined;
  if (mInfo && typeof mInfo === "object") {
    if (mInfo.category === "video") return true;
    if (Array.isArray(mInfo.modalities?.output) && mInfo.modalities.output.includes("video")) {
      return true;
    }
  }
  return false;
}

export function hasConfiguredImageGenerationModel(): boolean {
  for (const prov of workspace.providers) {
    if (!prov || prov.id === AUTO_PROVIDER_ID) continue;
    for (const mid of Object.keys(prov.models || {})) {
      if (isImageGenerationModel(prov.id, mid)) return true;
    }
  }
  for (const [pid, pVal] of Object.entries(workspace.hoxiProviders || {})) {
    if (!pid || pid === AUTO_PROVIDER_ID || !pVal) continue;
    for (const mid of [
      ...Object.keys(pVal.models || {}),
      ...Object.keys((pVal as Record<string, any>).mediaModels || {}),
    ]) {
      if (isImageGenerationModel(pid, mid)) return true;
    }
  }
  return false;
}

export function hasConfiguredVideoGenerationModel(): boolean {
  for (const prov of workspace.providers) {
    if (!prov || prov.id === AUTO_PROVIDER_ID) continue;
    for (const mid of Object.keys(prov.models || {})) {
      if (isVideoGenerationModel(prov.id, mid)) return true;
    }
  }
  for (const [pid, pVal] of Object.entries(workspace.hoxiProviders || {})) {
    if (!pid || pid === AUTO_PROVIDER_ID || !pVal) continue;
    for (const mid of [
      ...Object.keys(pVal.models || {}),
      ...Object.keys((pVal as Record<string, any>).mediaModels || {}),
    ]) {
      if (isVideoGenerationModel(pid, mid)) return true;
    }
  }
  return false;
}

export function isSpeechSynthesisModel(
  providerID?: string | null,
  modelID?: string | null
): boolean {
  const mid = (modelID || "").trim();
  if (!mid) return false;
  if (inferDedicatedMediaModality(mid) === "audio") return true;
  const pid = (providerID || "").trim();
  if (!pid) return false;
  const hoxiModel =
    workspace.hoxiProviders?.[pid]?.models?.[mid] ??
    workspace.hoxiProviders?.[pid]?.mediaModels?.[mid];
  if (hoxiModel && typeof mCategory(hoxiModel) === "string") {
    if (mCategory(hoxiModel) === "tts") return true;
    if (Array.isArray(hoxiModel.modalities?.output) && hoxiModel.modalities.output.includes("audio")) {
      return true;
    }
  }
  const prov = workspace.providers.find((p) => p.id === pid);
  const mInfo = prov?.models?.[mid] as Record<string, any> | undefined;
  if (mInfo && typeof mInfo === "object") {
    if (mInfo.category === "tts") return true;
    if (Array.isArray(mInfo.modalities?.output) && mInfo.modalities.output.includes("audio")) {
      return true;
    }
  }
  return false;
}

function mCategory(m: Record<string, any>): string {
  return typeof m?.category === "string" ? m.category.trim().toLowerCase() : "";
}

export function hasConfiguredSpeechModel(): boolean {
  for (const prov of workspace.providers) {
    if (!prov || prov.id === AUTO_PROVIDER_ID) continue;
    for (const mid of Object.keys(prov.models || {})) {
      if (isSpeechSynthesisModel(prov.id, mid)) return true;
    }
  }
  for (const [pid, pVal] of Object.entries(workspace.hoxiProviders || {})) {
    if (!pid || pid === AUTO_PROVIDER_ID || !pVal) continue;
    for (const mid of [
      ...Object.keys(pVal.models || {}),
      ...Object.keys((pVal as Record<string, any>).mediaModels || {}),
    ]) {
      if (isSpeechSynthesisModel(pid, mid)) return true;
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
  /\b(?:generate|create|render|make)\s+(?:a\s+)?(?:video|storyboard|clip)\b|(?:\u751f\u6210|\u5236\u4f5c|\u505a(?:\u4e00?\u4e2a|\u4e00?\u6761|\u4e00?\u6bb5|\u51fa)?|\u6e32\u67d3|\u521b\u4f5c|\u51fa(?:\u4e00?\u4e2a|\u4e00?\u6761|\u4e00?\u6bb5))[\u4e00-\u9fff0-9:：\s]{0,12}(?:\u89c6\u9891|\u77ed\u89c6\u9891|\u5206\u955c\u89c6\u9891|\u77ed\u5267|\u9996\u5c3e\u5e27\u89c6\u9891)|(?:^|[\s\uff0c\u3002\uff01\uff1f,.;:])(?:\u5e2e\u6211|\u8bf7|\u53bb|\u6765)?\u751f\u89c6\u9891(?=$|[\s\uff0c\u3002\uff01\uff1f,.;:\uff1a\u5427\u5417\u4e00\u4e0b])|\u77ed\u89c6\u9891\u811a\u672c/i;
const VIDEO_SKILL_OR_CHIP_RE =
  /\b(?:storyboard|seedance|kling|veo|sora|wanx)\b|\u751f\u89c6\u9891|\u751f\u6210\u89c6\u9891|\u77ed\u89c6\u9891\u811a\u672c|\u5206\u955c\u89c6\u9891|\u77ed\u5267|\u9996\u5c3e\u5e27|\u53ef\u7075|\u4e07\u76f8/i;
const VISUAL_REVIEW_INTENT_RE =
  /(?:\u5e2e\u6211|\u8bf7|\u505a|\u8fdb\u884c|\u6267\u884c|\u5f00\u59cb)[\u4e00-\u9fff\s]{0,6}(?:\u89c6\u89c9\u590d\u76d8|\u81ea\u52a8\u590d\u76d8|\u5ba1\u56fe|\u5c01\u9762\u8bca\u65ad|\u5b89\u5168\u533a\u68c0\u67e5|\u906e\u6321\u68c0\u67e5)|\b(?:run|do|perform)\s+(?:a\s+)?(?:visual\s+review|ui\s+audit)\b/i;
const VISUAL_REVIEW_SKILL_OR_CHIP_RE =
  /\u89c6\u89c9\u590d\u76d8|\u81ea\u52a8\u590d\u76d8|\u5ba1\u56fe|\u5c01\u9762\u8bca\u65ad|\u5b89\u5168\u533a\u68c0\u67e5|\u906e\u6321\u68c0\u67e5|\bvisual\s+review\b|\bui\s+audit\b/i;
const IMAGE_INTENT_RE =
  /\b(?:generate|create|draw|render)\s+(?:an?\s+)?(?:image|illustration|poster|cover|banner|infographic)\b|(?:\u751f\u6210|\u753b|\u7ed8\u5236|\u5236\u4f5c|\u505a(?:\u4e00?\u5f20|\u4e2a|\u5957|\u51e0\u5f20|\u51fa)?|\u8bbe\u8ba1|\u6e32\u67d3|\u4ea7\u51fa|\u91cd\u7ed8|\u51fa(?:\u4e00?\u5f20|\u5f20|\u4e2a|\u5957|\u51e0\u5f20))[\u4e00-\u9fff0-9:：\s]{0,12}(?:\u56fe\u7247|\u4e3b\u56fe|\u5c01\u9762|\u767d\u5e95\u56fe|\u573a\u666f\u56fe|\u6d77\u62a5\u56fe|\u6d77\u62a5|\u9996\u5c4f\u56fe\u5361|\u56fe\u5361|\u914d\u56fe|\u63d2\u753b)|(?:^|[\s\uff0c\u3002\uff01\uff1f,.;:])(?:\u5e2e\u6211|\u8bf7|\u53bb|\u6765)?\u751f\u56fe(?=$|[\s\uff0c\u3002\uff01\uff1f,.;:\uff1a\u5427\u5417\u4e00\u4e0b])/i;
const IMAGE_SKILL_OR_CHIP_RE =
  /hero-card\.svg|\u751f\u56fe|\u751f\u6210\u56fe\u7247|\u751f\u6210\u4e3b\u56fe|\u751f\u6210\u5c01\u9762|3:4\s*\u7ad6\u7248\u5c01\u9762|\u767d\u5e95\u56fe|\u573a\u666f\u56fe|\u6d77\u62a5\u56fe|\u9996\u5c4f\u56fe\u5361/i;
const AUDIO_INTENT_RE =
  /\b(?:generate|create|synthesize|record|make)\s+(?:a\s+|an\s+)?(?:[\w-]+\s+){0,2}(?:voiceover|tts|audio|speech)\b|(?:\u751f\u6210|\u5408\u6210|\u5236\u4f5c|\u5f55\u5236|\u505a(?:\u4e00?\u6bb5|\u4e00?\u4e2a|\u51fa)?|\u5e2e\u6211|\u8bf7\u7ed9)[\u4e00-\u9fff0-9\s]{0,8}(?:\u914d\u97f3|\u53e3\u64ad\u97f3\u9891|\u8bed\u97f3\u5408\u6210|\u97f3\u9891|\u65c1\u767d)/i;
const AUDIO_SKILL_OR_CHIP_RE =
  /\u914d\u97f3|\u53e3\u64ad\u97f3\u9891|\u8bed\u97f3\u5408\u6210|\u751f\u6210\u97f3\u9891|\b(?:elevenlabs|fishaudio|tts|voiceover)\b/i;
const VERTICAL_ASPECT_RE = /\bportrait\b|3:4|\u7ad6\u7248/i;

type PromptIntentOptions = {
  hasImageAttachment?: boolean;
  skillName?: string;
  categoryId?: string;
  chipId?: string;
};

// Media routing directives only apply to the turn that triggered them. Tagging
// them lets the runner deliver them with that turn's user input instead of the
// system prompt, so a one-off directive never invalidates the cached prefix.
export const TURN_DIRECTIVE_OPEN = "<hoxi_turn_directive>";
export const TURN_DIRECTIVE_CLOSE = "</hoxi_turn_directive>";

export function evaluatePromptModalityIntent(promptText: string, opts?: PromptIntentOptions): PromptIntentEvaluation {
  const res = evaluatePromptModalityIntentUntagged(promptText, opts);
  if (!res.mediaRoutingDirective) return res;
  return {
    ...res,
    mediaRoutingDirective: `${TURN_DIRECTIVE_OPEN}\n${res.mediaRoutingDirective}\n${TURN_DIRECTIVE_CLOSE}`,
  };
}

// Match requested actions within a clause so a negation or a reference to an API
// cannot select a costly media route for a separate coding request.
const NEGATED_MEDIA_ACTION = /(?:\u4e0d\u8981|\u4e0d\u9700\u8981|\u65e0\u9700|\u4e0d\u7528|(?:^|[^\u7279\u5206\u8bc6])\u522b(?=\u518d|\u5e2e|\u7ed9|\u505a|\u753b|\u751f\u6210|\u5236\u4f5c|\u5408\u6210|\u6e32\u67d3|\u5f55\u5236|\u5ba1\u56fe)|\u4e0d(?=\u751f\u6210|\u5236\u4f5c|\u5408\u6210|\u6e32\u67d3|\u5f55\u5236)|\u7981\u6b62|\u907f\u514d)|\b(?:do\s+not|don['\u2019]t|never|avoid|without|skip)\b/i;
const MEDIA_DEBUG_ACTION = /(?:\u4fee\u590d|\u6392\u67e5|\u8c03\u8bd5|\u91cd\u6784|\u8bca\u65ad|\u5b9a\u4f4d)|\b(?:fix|debug|troubleshoot|refactor|diagnose)\b/i;
const MEDIA_CODE_OBJECT = /^\s*(?:\u7684?\s*(?:\u63a5\u53e3|\u6a21\u5757|\u51fd\u6570|\u7ec4\u4ef6|\u6309\u94ae|\u529f\u80fd|\u7ba1\u7ebf)|(?:api|endpoint|function|method|button|cache|pipeline)\b)/i;

function requestsMedia(clauses: string[], action: RegExp, selection: RegExp, explicitMeta: string): boolean {
  let declined = false;
  for (const clause of clauses) {
    const match = action.exec(clause);
    if (!match) {
      if (NEGATED_MEDIA_ACTION.test(clause) && selection.test(clause)) declined = true;
      continue;
    }
    const politePrefix = /^(?:\s|\u5e2e\u6211|\u8bf7\u7ed9|\u8bf7)*/.exec(match[0]);
    const actionStart = match.index + (politePrefix?.[0].length ?? 0);
    const negation = NEGATED_MEDIA_ACTION.exec(clause);
    if (negation && negation.index <= actionStart) {
      declined = true;
      continue;
    }
    const debugging = MEDIA_DEBUG_ACTION.exec(clause);
    if (debugging && debugging.index <= actionStart) continue;
    if (!MEDIA_CODE_OBJECT.test(clause.slice(match.index + match[0].length))) return true;
  }
  return !declined && (selection.test(explicitMeta) || action.test(explicitMeta));
}

function evaluatePromptModalityIntentUntagged(promptText: string, opts?: PromptIntentOptions): PromptIntentEvaluation {
  const raw = parseHoxiContextPrompt(promptText || "").bodyText.trim();
  const clauses = raw
    .replace(/```[\s\S]*?(?:```|$)|`[^`\n]*`/g, " ")
    .replace(/"[^"\n]*"|\u201c[^\u201d\n]*\u201d|\u2018[^\u2019\n]*\u2019|\u300c[^\u300d\n]*\u300d/g, " ")
    .replace(/(^|[\s(])'[^'\n]+'(?=$|[\s.,;!?)])/g, "$1 ")
    .split(/[,;.!?\uff0c\u3002\uff1b\uff01\uff1f\n]|\u7136\u540e|\u540e\u518d|\u800c\u662f|\b(?:and then|but)\b/i);
  const explicitMeta = `${opts?.skillName || ""} ${opts?.categoryId || ""} ${opts?.chipId || ""}`.trim();
  const combined = explicitMeta ? `${raw} ${explicitMeta}` : raw;

  if (requestsMedia(clauses, VIDEO_INTENT_RE, VIDEO_SKILL_OR_CHIP_RE, explicitMeta)) {
    const hasLocalVideo = hasConfiguredVideoGenerationModel();
    return {
      kind: "video_gen",
      requiresVisionBrain: true,
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-video-seedance",
      mediaModality: "video",
      defaultAspectRatio: "9:16",
      mediaRoutingDirective: hasLocalVideo
        ? "[Modality Router: Video & Storyboard Generation]\n- Keep the session orchestrator on a tool-calling Vision chat model.\n- When the user is requesting video rendering (not merely auditing code or reviewing a report): Invoke `hoxi_video` (checks the active model's native video capability first, then configured local video models) to render and save video to disk; fall back to `hoxi_tools` only if local rendering is unavailable."
        : "[Modality Router: Video & Storyboard Generation]\n- Keep the session orchestrator on a tool-calling Vision chat model.\n- When the user is requesting video creation, structure shot lists and `.srt` captions in the workspace; invoke `hoxi_video` if a local video model is configured, otherwise invoke `hoxi_tools` video synthesis endpoints when external video rendering is requested.",
    };
  }

  if (requestsMedia(clauses, VISUAL_REVIEW_INTENT_RE, VISUAL_REVIEW_SKILL_OR_CHIP_RE, explicitMeta)) {
    return {
      kind: "visual_review",
      requiresVisionBrain: true,
      recommendedPoolID: "frontier",
      logicalMediaModelID: "hoxi-image-pro",
      mediaModality: "image",
      defaultAspectRatio: "3:4",
      mediaRoutingDirective:
        "[Modality Router: Automated Visual Review]\n- Use Vision inspection capabilities to verify aspect ratios (1:1 / 3:4 / 9:16), UI safe-zone clearance, reference asset fidelity, layout alignment, and contrast hierarchy.",
    };
  }

  if (requestsMedia(clauses, IMAGE_INTENT_RE, IMAGE_SKILL_OR_CHIP_RE, explicitMeta)) {
    const isVertical = VERTICAL_ASPECT_RE.test(combined);
    const ratio = isVertical ? "3:4" : "1:1";
    const hasLocalImage = hasConfiguredImageGenerationModel();
    return {
      kind: "image_gen",
      requiresVisionBrain: true,
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-image-pro",
      mediaModality: "image",
      defaultAspectRatio: ratio,
      mediaRoutingDirective: hasLocalImage
        ? "[Modality Router: Image & Visual Asset Generation]\n- When the user is asking to create or edit a visual deliverable (not merely auditing code or reviewing a pasted report), do NOT stop at a text-only description — invoke `hoxi_image` (checks the active model's native capability first, then configured local image models) to save the visual output to disk."
        : "[Modality Router: Image & Visual Asset Generation]\n- When the user is asking to create or edit a visual deliverable (not merely auditing code or reviewing a pasted report), do NOT stop at a text-only description — invoke `hoxi_image` (or generate deterministic SVG assets in the workspace, or fall back to `hoxi_tools` when no local raster image model is configured) to save the visual output to disk.",
    };
  }

  if (requestsMedia(clauses, AUDIO_INTENT_RE, AUDIO_SKILL_OR_CHIP_RE, explicitMeta)) {
    const hasLocalSpeech = hasConfiguredSpeechModel();
    return {
      kind: "audio_tts",
      requiresVisionBrain: Boolean(opts?.hasImageAttachment),
      recommendedPoolID: "daily",
      logicalMediaModelID: "hoxi-audio-tts",
      mediaModality: "audio",
      defaultAspectRatio: "9:16",
      mediaRoutingDirective: hasLocalSpeech
        ? "[Modality Router: Voiceover & Subtitle Alignment]\n- When the user is requesting spoken audio output (not merely auditing code or reviewing a report), generate time-aligned `.srt` subtitles in the workspace and invoke `hoxi_speech` (checks the active model's native audio capability first, then configured local TTS models) to save spoken audio to disk; fall back to `hoxi_tools` only if unavailable."
        : "[Modality Router: Voiceover & Subtitle Alignment]\n- When the user is requesting spoken audio output, generate time-aligned `.srt` subtitles in the workspace; invoke `hoxi_speech` if a local TTS model is configured, otherwise invoke `hoxi_tools` (`elevenlabs` / `fishaudio`) when audio synthesis is requested.",
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

export type AttachmentModality = "image" | "pdf" | "audio" | "video";

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

export function modelSupportsModalityInput(
  providerID: string,
  modelID: string,
  modality: AttachmentModality
): boolean {
  if (modality === "image") return modelSupportsImageInput(providerID, modelID);
  if (!providerID || !modelID) return false;
  if (isNonChatUtilityModelID(modelID) || isImageGenerationModel(providerID, modelID)) {
    return false;
  }
  const hoxiModel = workspace.hoxiProviders?.[providerID]?.models?.[modelID];
  if (hoxiModel && typeof hoxiModel === "object") {
    if (hoxiModel.capabilities?.input?.[modality] === true) return true;
    if (Array.isArray(hoxiModel.modalities?.input)) {
      return hoxiModel.modalities.input.includes(modality);
    }
    if (hoxiModel.capabilities?.input?.[modality] === false) return false;
  }
  const prov = workspace.providers.find((p) => p.id === providerID);
  const mInfo = prov?.models?.[modelID];
  if (mInfo && typeof mInfo === "object") {
    if (mInfo.capabilities?.input?.[modality] === true) return true;
    if (Array.isArray(mInfo.modalities?.input)) {
      return mInfo.modalities.input.includes(modality);
    }
    if (mInfo.capabilities?.input?.[modality] === false) return false;
  }
  const lowerMod = modelID.toLowerCase();
  const lowerProv = providerID.toLowerCase();
  const isGemini =
    lowerMod.includes("gemini") ||
    lowerProv.includes("google") ||
    lowerProv.includes("gemini") ||
    prov?.npm === "@ai-sdk/google";
  if (modality === "pdf") {
    if (isGemini) return true;
    if (
      lowerMod.includes("claude-3-5") ||
      lowerMod.includes("claude-3-7") ||
      /claude-(?:sonnet-|opus-|haiku-|fable-)?[4-9]/.test(lowerMod) ||
      prov?.npm === "@ai-sdk/anthropic"
    ) {
      return true;
    }
    return /^(gpt-4o|chatgpt-4o|gpt-4\.1|gpt-[5-9]|o3|o4)/i.test(lowerMod);
  }
  if (modality === "audio") {
    return isGemini || lowerMod.includes("omni") || lowerMod.includes("audio");
  }
  if (modality === "video") {
    return (
      isGemini ||
      lowerMod.includes("omni") ||
      lowerMod.includes("qwen2.5-vl") ||
      lowerMod.includes("qwen3-vl") ||
      /qwen-?3\.[5-9]|qwen-?[4-9]/.test(lowerMod)
    );
  }
  return false;
}

export function detectRequiredAttachmentModalities(
  attachments?: Array<{ mime?: string; filename?: string; url?: string }>
): AttachmentModality[] {
  if (!Array.isArray(attachments) || attachments.length === 0) return [];
  const found = new Set<AttachmentModality>();
  for (const att of attachments) {
    const mime = (att?.mime || "").toLowerCase().trim();
    const url = (att?.url || "").toLowerCase().trim();
    const name = (att?.filename || "").toLowerCase().trim();
    if (
      mime.startsWith("image/") ||
      url.startsWith("data:image/") ||
      /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/.test(name)
    ) {
      found.add("image");
    } else if (
      mime === "application/pdf" ||
      url.startsWith("data:application/pdf") ||
      name.endsWith(".pdf")
    ) {
      found.add("pdf");
    } else if (
      mime.startsWith("audio/") ||
      url.startsWith("data:audio/") ||
      /\.(mp3|wav|m4a|aac|ogg|oga|opus|flac)$/.test(name)
    ) {
      found.add("audio");
    } else if (
      mime.startsWith("video/") ||
      url.startsWith("data:video/") ||
      /\.(mp4|mov|webm|mkv|avi)$/.test(name)
    ) {
      found.add("video");
    }
  }
  return [...found];
}
