// web/src/components/share/share-image-model.ts
// Pure, DOM-free session data preparation for shareable PNG image cards:
// exchange grouping, structured context/quote & collab parsing, privacy
// redaction, local path shortening, tool pill summarization, and image collection.
import type { MessageInfo, MessagePart, SessionInfo, ToolPart } from "../../services/sse-types";
import {
  getContextItemBodyText,
  parseCollabPrompt,
  parseHoxiContextPrompt,
  prettyFormatContextBody,
  type CollabPromptRole,
  type ContextEnvelope,
  type ContextItem,
} from "../../store/workspace";
import { expandInlineThinkingParts, extractVisibleAssistantText } from "../timeline/context-activity-parts";
import { replaceRichFencesForReadableExport } from "../timeline/rich-view-spec";
import { normalizeReasoningText } from "../timeline/ThoughtBox";
import { formatToolDisplayName, isTodoTool, meaningfulToolTitle } from "../timeline/tool-taxonomy";

export interface ShareTurnInput {
  info: MessageInfo;
  parts: MessagePart[];
}

export interface ShareToolPill {
  id: string;
  label: string;
  detail: string;
  status: "completed" | "error" | "running" | "pending";
}

export interface ShareImageAsset {
  id: string;
  exchangeID: string;
  role: "user" | "assistant";
  label: string;
  src: string;
  /** Path-shortened/cleaned src matching the transformed markdown text. */
  displaySrc: string;
  sourceKind: "attachment" | "markdown" | "data" | "remote";
  mime?: string;
}

export interface ShareContextKeyValue {
  key: string;
  value: string;
}

export interface ShareContextItem {
  kind: ContextItem["kind"];
  title?: string;
  subtitle?: string;
  badge?: string;
  refLabel?: string;
  body?: string;
  isJson?: boolean;
  kvPairs?: ShareContextKeyValue[];
}

export interface ShareContextBlock {
  sourceLabel?: string;
  tabTitle?: string;
  isSingleQuote: boolean;
  items: ShareContextItem[];
}

export type ShareCollabRole = CollabPromptRole | "op";

export interface ShareCollabInfo {
  role: ShareCollabRole;
  author?: string;
  mode?: "plan" | "build";
  title?: string;
  sections: Array<{ heading: string; content: string }>;
}

export interface ShareExchange {
  id: string;
  index: number;
  messageIDs: string[];
  createdAt?: number;
  modelID?: string;
  userText: string;
  userPreview: string;
  userContexts: ShareContextBlock[];
  userCollab?: ShareCollabInfo;
  assistantText: string;
  assistantPreview: string;
  thinkingText: string;
  tools: ShareToolPill[];
  images: ShareImageAsset[];
  redactionCount: number;
}

export interface ShareBuildOptions {
  selectedExchangeIDs?: ReadonlySet<string>;
  disabledImageIDs?: ReadonlySet<string>;
  includeTools: boolean;
  includeThinking: boolean;
  includeImages: boolean;
  hideLocalPaths: boolean;
  redactSecrets: boolean;
  workspaceDir?: string;
}

export interface ShareDocument {
  sessionID: string;
  title: string;
  runtime: string;
  modelID: string;
  projectLabel: string;
  createdAt?: number;
  updatedAt?: number;
  exchanges: ShareExchange[];
  allImages: ShareImageAsset[];
  redactionCount: number;
}

// ---------------------------------------------------------------------------
// Privacy redaction (mirrors internal/diagnostics/redact.go rules)
// ---------------------------------------------------------------------------

const REDACTED = "[redacted]";

const SECRET_KEYS =
  "api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|" +
  "opencode_server_password|password|passwd|passphrase|sshpass|secret|token|authorization|credentials?";
const SHORT_SECRET_KEYS = "pass|pwd|pw|auth";
const SECRET_KEY_PREFIX = `((?:^|[^a-z0-9$])(?:${SHORT_SECRET_KEYS})|${SECRET_KEYS})(["']?\\s*[=:]\\s*)`;

const PRIVATE_KEY_BLOCK_RE =
  /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g;
const SECRET_TOKEN_RE =
  /\bbearer\s+[^\s"'`]+|\b(?:sk-ant-|sk-proj-|sk-|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xox[abprs]-|hk-)[a-z0-9_\-]{8,}|\bAKIA[0-9A-Z]{16}\b/gi;
const URL_USERINFO_RE = /\b([a-z][a-z0-9+.-]*:\/\/[^\s/?#@:'"]*):([^\s/?#@'"]+)@/gi;
const CURL_USER_RE = /(\bcurl\b[^|;&\n]*?\s(?:-u\s*|--user(?:\s+|=))["']?[^\s:"']*):([^\s"']+)/g;
const MYSQL_PASSWORD_RE =
  /(\b(?:mysql|mysqldump|mysqladmin|mysqlimport|mysqlcheck|mysqlshow|mysqlsh|mariadb|mariadb-dump)\b[^|;&\n]*?\s-p)(?:"[^"]*"|'[^']*'|[^\s"']+)/g;
const FLAG_SECRET_RE =
  /(--(?:password|passwd|pass|passphrase|token|auth-token|access-token|api-key|secret|client-secret)\s+)(?:"[^"]*"|'[^']*'|[^\s"'-][^\s"']*)/gi;
const SECRET_QUOTED_RE = new RegExp(
  `${SECRET_KEY_PREFIX}(?:(")([^"]*)(")|(')([^']*)('))`,
  "gi"
);
const SECRET_BARE_RE = new RegExp(
  `${SECRET_KEY_PREFIX}(?:(?:basic|bearer|digest|token)\\s+)?([^\\s,;"'}&|)<>]+)`,
  "gi"
);

export function redactShareSecrets(input: string): { text: string; count: number } {
  if (!input) return { text: "", count: 0 };
  let count = 0;
  let text = input;

  text = text.replace(PRIVATE_KEY_BLOCK_RE, (m) => {
    if (m === REDACTED) return m;
    count++;
    return REDACTED;
  });
  text = text.replace(SECRET_TOKEN_RE, (m) => {
    if (m === REDACTED || m.toLowerCase() === `bearer ${REDACTED}`) return m;
    count++;
    return REDACTED;
  });
  text = text.replace(URL_USERINFO_RE, (m, prefix: string, secret: string) => {
    if (secret === REDACTED) return m;
    count++;
    return `${prefix}:${REDACTED}@`;
  });
  text = text.replace(CURL_USER_RE, (m, prefix: string, secret: string) => {
    if (secret === REDACTED) return m;
    count++;
    return `${prefix}:${REDACTED}`;
  });
  text = text.replace(MYSQL_PASSWORD_RE, (m, prefix: string) => {
    if (m.endsWith(REDACTED)) return m;
    count++;
    return `${prefix}${REDACTED}`;
  });
  text = text.replace(FLAG_SECRET_RE, (m, prefix: string) => {
    if (m.endsWith(REDACTED)) return m;
    count++;
    return `${prefix}${REDACTED}`;
  });
  text = text.replace(
    SECRET_QUOTED_RE,
    (m, k: string, sep: string, dq1: string, dv: string, dq2: string, sq1: string, sv: string, sq2: string) => {
      const val = dq1 ? dv : sv;
      if (!val || val === REDACTED) return m;
      count++;
      return dq1 ? `${k}${sep}${dq1}${REDACTED}${dq2}` : `${k}${sep}${sq1}${REDACTED}${sq2}`;
    }
  );
  text = text.replace(SECRET_BARE_RE, (m, k: string, sep: string, val: string) => {
    if (!val || val === REDACTED) return m;
    count++;
    return `${k}${sep}${REDACTED}`;
  });

  return { text, count };
}

// ---------------------------------------------------------------------------
// Local path shortening
// ---------------------------------------------------------------------------

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function shortenLocalPaths(input: string, workspaceDir?: string): string {
  if (!input) return "";
  let out = input;

  const cleanDir = (workspaceDir || "").trim().replace(/[\\/]+$/, "");
  if (cleanDir && cleanDir.length > 2) {
    const posixDir = cleanDir.replace(/\\/g, "/");
    const winDir = cleanDir.replace(/\//g, "\\");
    const variants = new Set<string>([cleanDir, posixDir]);
    if (winDir !== posixDir) variants.add(winDir);
    const wtIdx = posixDir.indexOf("/.hoxi/worktrees/");
    if (wtIdx > 0) {
      const repoRoot = posixDir.slice(0, wtIdx);
      variants.add(repoRoot);
      variants.add(repoRoot.replace(/\//g, "\\"));
    }
    const sorted = Array.from(variants).sort((a, b) => b.length - a.length);
    for (const root of sorted) {
      if (root.length < 3) continue;
      const re = new RegExp(`${escapeRegExp(root)}(?:[\\\\/]|(?=$|[\\s"'\\)\\],;:]))`, "g");
      out = out.replace(re, (match) => {
        const trailing = match.endsWith("/") || match.endsWith("\\") ? "/" : "";
        return `<workspace>${trailing}`;
      });
    }
  }

  out = out.replace(/\/Users\/[^/\s"'`<>|:,;)\]]+(?=\/|$)/g, "~");
  out = out.replace(/\/home\/[^/\s"'`<>|:,;)\]]+(?=\/|$)/g, "~");
  out = out.replace(/[A-Za-z]:\\Users\\[^\\\s"'`<>|:,;)\]]+(?=\\|$)/g, "~");
  return out;
}

// ---------------------------------------------------------------------------
// Tool summary extraction, context normalization & image extraction
// ---------------------------------------------------------------------------

function basenameOf(raw: string): string {
  const cleaned = raw.trim().replace(/[\\/]+$/, "");
  if (!cleaned) return "";
  const segs = cleaned.split(/[\\/]/);
  return segs[segs.length - 1] || cleaned;
}

function truncateInline(value: string, max: number): string {
  const single = value.replace(/\s+/g, " ").trim();
  if (single.length <= max) return single;
  return `${single.slice(0, Math.max(1, max - 1))}…`;
}

function extractFlatJsonKeyValues(raw: string): ShareContextKeyValue[] | undefined {
  const trimmed = raw.trim();
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) return undefined;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
    const entries = Object.entries(parsed as Record<string, unknown>);
    if (entries.length === 0 || entries.length > 8) return undefined;
    const out: ShareContextKeyValue[] = [];
    for (const [k, v] of entries) {
      if (!k || k.length > 36) return undefined;
      if (v === null || v === undefined) continue;
      if (typeof v === "object") return undefined;
      const str = String(v).trim();
      if (!str || str.length > 180 || str.includes("\n")) return undefined;
      out.push({ key: k, value: str });
    }
    return out.length > 0 ? out : undefined;
  } catch {
    return undefined;
  }
}

function buildShareContextBlock(
  env: ContextEnvelope,
  cleanText: (raw: string) => string
): ShareContextBlock | null {
  const items: ShareContextItem[] = [];
  for (const item of env.items) {
    const title = item.title ? cleanText(item.title) : undefined;
    const subtitle = item.subtitle ? cleanText(item.subtitle) : undefined;
    const badge = item.badge ? cleanText(item.badge) : undefined;
    const rawRef = item.ref?.id ? (item.entityType ? `${item.entityType}:${item.ref.id}` : `#${item.ref.id}`) : undefined;
    const refLabel = rawRef ? cleanText(rawRef) : undefined;
    const rawBody = getContextItemBodyText(item).trim();

    let body: string | undefined;
    let isJson = false;
    let kvPairs: ShareContextKeyValue[] | undefined;

    if (rawBody) {
      const formatted = prettyFormatContextBody(rawBody);
      isJson = formatted.isJson;
      if (formatted.isJson) {
        const rawKv = extractFlatJsonKeyValues(rawBody);
        if (rawKv) {
          // Run cleanText on key: value pairs so secret-key redaction catches sensitive keys too.
          kvPairs = rawKv.map((entry) => {
            const cleanedPair = cleanText(`${entry.key}: ${entry.value}`);
            const sepIdx = cleanedPair.indexOf(": ");
            if (sepIdx > 0) {
              return {
                key: cleanedPair.slice(0, sepIdx).trim(),
                value: cleanedPair.slice(sepIdx + 2).trim(),
              };
            }
            return { key: cleanText(entry.key), value: cleanText(entry.value) };
          });
        } else {
          body = cleanText(formatted.text);
        }
      } else {
        body = cleanText(formatted.text);
      }
    }

    if (!title && !subtitle && !badge && !refLabel && !body && !kvPairs?.length) continue;
    items.push({
      kind: item.kind,
      ...(title ? { title } : {}),
      ...(subtitle ? { subtitle } : {}),
      ...(badge ? { badge } : {}),
      ...(refLabel ? { refLabel } : {}),
      ...(body ? { body } : {}),
      ...(isJson ? { isJson: true } : {}),
      ...(kvPairs ? { kvPairs } : {}),
    });
  }
  if (items.length === 0) return null;
  const rawSource = env.source?.appName || env.source?.appId;
  const sourceLabel = rawSource ? cleanText(rawSource) : undefined;
  const tabTitle = env.source?.tabTitle ? cleanText(env.source.tabTitle) : undefined;
  return {
    ...(sourceLabel ? { sourceLabel } : {}),
    ...(tabTitle ? { tabTitle } : {}),
    isSingleQuote: items.length === 1 && items[0].kind === "quote",
    items,
  };
}

function extractToolDetail(part: ToolPart): string {
  const state = part.state;
  const input = (state.input || {}) as Record<string, unknown>;

  const title = meaningfulToolTitle(state.title, part.tool);
  if (title) return truncateInline(title, 54);

  const fileCandidate =
    input.filePath ?? input.file_path ?? input.path ?? input.target_file ?? input.TargetFile ?? input.Path;
  if (typeof fileCandidate === "string" && fileCandidate.trim()) {
    return truncateInline(basenameOf(fileCandidate), 48);
  }

  const cmdCandidate = input.command ?? input.CommandLine ?? input.cmd;
  if (typeof cmdCandidate === "string" && cmdCandidate.trim()) {
    return truncateInline(cmdCandidate, 52);
  }

  const queryCandidate = input.pattern ?? input.query ?? input.Query ?? input.url ?? input.Url;
  if (typeof queryCandidate === "string" && queryCandidate.trim()) {
    return truncateInline(queryCandidate, 48);
  }

  return "";
}

function summarizeToolPart(part: ToolPart): ShareToolPill | null {
  if (isTodoTool(part.tool)) return null;
  const label = formatToolDisplayName(part.tool, part) || part.tool;
  const detail = extractToolDetail(part);
  return {
    id: part.id || part.callID || `${part.tool}_${detail}`,
    label,
    detail,
    status: part.state.status,
  };
}

function isImageFilePart(mime?: string, url?: string, filename?: string): boolean {
  const m = (mime || "").toLowerCase();
  if (m.startsWith("image/")) return true;
  if (/^data:image\//i.test(url || "")) return true;
  return /\.(png|jpe?g|webp|gif|bmp|avif|svg)$/i.test(filename || "");
}

function classifyImageSource(src: string): ShareImageAsset["sourceKind"] {
  const s = src.trim();
  if (/^data:image\//i.test(s)) return "data";
  if (s.startsWith("/api/hoxi/")) return "attachment";
  if (/^https?:\/\//i.test(s)) return "remote";
  return "markdown";
}

const MARKDOWN_IMAGE_RE =
  /!\[([^\]]*)\]\(((?:[^()\s]|\([^()\s]*\))+)(?:\s+(?:"[^"]*"|'[^']*'))?\)/g;

function extractMarkdownImages(text: string): Array<{ alt: string; src: string }> {
  const results: Array<{ alt: string; src: string }> = [];
  if (!text || !text.includes("![")) return results;
  const stripped = text.replace(/```[\s\S]*?```/g, "");
  MARKDOWN_IMAGE_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = MARKDOWN_IMAGE_RE.exec(stripped)) !== null) {
    const src = (match[2] || "").trim();
    if (!src) continue;
    results.push({ alt: (match[1] || "").trim(), src });
  }
  return results;
}

function makePreviewLine(text: string, fallback: string): string {
  const cleaned = text
    .replace(/```[\s\S]*?```/g, " [code] ")
    .replace(/!\[[^\]]*\]\([^)]+\)/g, " [image] ")
    .replace(/[#>*`_~]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!cleaned) return fallback;
  return truncateInline(cleaned, 72);
}

function summarizeContextPreview(blocks: ShareContextBlock[]): string {
  const first = blocks[0]?.items[0];
  if (!first) return "";
  const candidate =
    first.title ||
    first.subtitle ||
    first.body ||
    (first.kvPairs?.map((kv) => `${kv.key}: ${kv.value}`).join(" · ") ?? "") ||
    first.refLabel ||
    "";
  return makePreviewLine(candidate, "");
}

// ---------------------------------------------------------------------------
// Exchange grouping (one User prompt + its subsequent Assistant step cycle)
// ---------------------------------------------------------------------------

export function groupTurnsIntoExchanges(
  turns: ShareTurnInput[],
  options: { hideLocalPaths: boolean; redactSecrets: boolean; workspaceDir?: string }
): { exchanges: ShareExchange[]; allImages: ShareImageAsset[]; redactionCount: number } {
  const exchanges: ShareExchange[] = [];
  const allImages: ShareImageAsset[] = [];
  let totalRedactions = 0;

  const cleanText = (raw: string): string => {
    let value = raw;
    if (options.hideLocalPaths) value = shortenLocalPaths(value, options.workspaceDir);
    if (options.redactSecrets) {
      const res = redactShareSecrets(value);
      value = res.text;
      totalRedactions += res.count;
    }
    return value;
  };

  interface RawGroup {
    userTurns: ShareTurnInput[];
    assistantTurns: ShareTurnInput[];
  }

  const groups: RawGroup[] = [];
  let current: RawGroup | null = null;

  for (const turn of turns) {
    if (turn.info.role === "user") {
      if (current && (current.userTurns.length > 0 || current.assistantTurns.length > 0)) {
        groups.push(current);
      }
      current = { userTurns: [turn], assistantTurns: [] };
    } else {
      if (!current) {
        current = { userTurns: [], assistantTurns: [turn] };
      } else {
        current.assistantTurns.push(turn);
      }
    }
  }
  if (current && (current.userTurns.length > 0 || current.assistantTurns.length > 0)) {
    groups.push(current);
  }

  for (let idx = 0; idx < groups.length; idx++) {
    const group = groups[idx];
    const messageIDs: string[] = [];
    const userChunks: string[] = [];
    const userContexts: ShareContextBlock[] = [];
    let userCollab: ShareCollabInfo | undefined;
    const assistantChunks: string[] = [];
    const thinkingChunks: string[] = [];
    const tools: ShareToolPill[] = [];
    const exchangeImages: ShareImageAsset[] = [];
    const seenImgSrc = new Set<string>();
    const redactionsBefore = totalRedactions;

    const firstTurn = group.userTurns[0] ?? group.assistantTurns[0];
    const exchangeID = firstTurn?.info.id || `exchange_${idx + 1}`;
    let modelID = "";
    let createdAt = firstTurn?.info.time?.created;

    const pushImage = (
      role: "user" | "assistant",
      src: string,
      label: string,
      mime?: string
    ) => {
      const trimmed = src.trim();
      if (!trimmed || seenImgSrc.has(trimmed)) return;
      seenImgSrc.add(trimmed);
      const displaySrc = options.hideLocalPaths ? shortenLocalPaths(trimmed, options.workspaceDir) : trimmed;
      const asset: ShareImageAsset = {
        id: `${exchangeID}_img_${exchangeImages.length + 1}`,
        exchangeID,
        role,
        label: truncateInline(label || basenameOf(trimmed) || "image", 40),
        src: trimmed,
        displaySrc,
        sourceKind: classifyImageSource(trimmed),
        mime,
      };
      exchangeImages.push(asset);
      allImages.push(asset);
    };

    for (const uTurn of group.userTurns) {
      messageIDs.push(uTurn.info.id);
      if (!createdAt && uTurn.info.time?.created) createdAt = uTurn.info.time.created;
      for (const part of uTurn.parts) {
        if (part.type === "text") {
          const rawText = part.text || "";
          const collab = parseCollabPrompt(rawText);
          if (collab) {
            const cleanSections = collab.sections.map((sec) => {
              for (const img of extractMarkdownImages(sec.content)) {
                pushImage("user", img.src, img.alt);
              }
              return {
                heading: cleanText(sec.heading),
                content: cleanText(sec.content),
              };
            });
            const cleanBody = cleanText(collab.bodyText.trim());
            const cleanTitle = collab.title ? cleanText(collab.title) : undefined;
            userCollab = {
              role: collab.role,
              ...(collab.author ? { author: cleanText(collab.author) } : {}),
              ...(collab.mode ? { mode: collab.mode } : {}),
              ...(cleanTitle && cleanTitle !== cleanBody ? { title: cleanTitle } : {}),
              sections: cleanSections,
            };
            if (cleanSections.length === 0 && cleanBody) {
              userChunks.push(cleanBody);
              for (const img of extractMarkdownImages(collab.bodyText)) {
                pushImage("user", img.src, img.alt);
              }
            }
            continue;
          }

          const parsed = parseHoxiContextPrompt(rawText);
          if (parsed.hasContext) {
            for (const env of parsed.envelopes) {
              const block = buildShareContextBlock(env, cleanText);
              if (block) userContexts.push(block);
            }
            const body = parsed.bodyText.trim();
            if (body) {
              userChunks.push(cleanText(body));
              for (const img of extractMarkdownImages(body)) {
                pushImage("user", img.src, img.alt);
              }
            }
            continue;
          }

          const plain = rawText.trim();
          if (plain) {
            userChunks.push(cleanText(plain));
            for (const img of extractMarkdownImages(plain)) {
              pushImage("user", img.src, img.alt);
            }
          }
        } else if (part.type === "file" && isImageFilePart(part.mime, part.url, part.filename)) {
          pushImage("user", part.url, part.filename || "attachment", part.mime);
        }
      }
    }

    for (const aTurn of group.assistantTurns) {
      messageIDs.push(aTurn.info.id);
      if (aTurn.info.modelID) modelID = aTurn.info.modelID;
      const expanded = expandInlineThinkingParts(aTurn.parts);
      for (const part of expanded) {
        if (part.type === "text") {
          const visible = replaceRichFencesForReadableExport(
            extractVisibleAssistantText(part.text).trim()
          );
          if (visible) {
            assistantChunks.push(cleanText(visible));
            for (const img of extractMarkdownImages(visible)) {
              pushImage("assistant", img.src, img.alt);
            }
          }
        } else if (part.type === "reasoning") {
          const norm = normalizeReasoningText(part.text).trim();
          if (norm) thinkingChunks.push(cleanText(norm));
        } else if (part.type === "file" && isImageFilePart(part.mime, part.url, part.filename)) {
          pushImage("assistant", part.url, part.filename || "image", part.mime);
        } else if (part.type === "tool") {
          const pill = summarizeToolPart(part);
          if (pill) {
            pill.detail = cleanText(pill.detail);
            tools.push(pill);
          }
          if (part.state.status === "completed" && Array.isArray(part.state.attachments)) {
            for (const att of part.state.attachments) {
              if (att.url && isImageFilePart(att.mime, att.url, att.filename)) {
                pushImage("assistant", att.url, att.filename || "generated-image", att.mime);
              }
            }
          }
        }
      }
    }

    const userText = userChunks.join("\n\n").trim();
    const assistantText = assistantChunks.join("\n\n").trim();
    const thinkingText = thinkingChunks.join("\n\n").trim();
    if (
      !userText &&
      userContexts.length === 0 &&
      !userCollab &&
      !assistantText &&
      !thinkingText &&
      tools.length === 0 &&
      exchangeImages.length === 0
    ) {
      continue;
    }

    const fallbackUserPreview =
      userCollab?.title ||
      userCollab?.sections[0]?.content ||
      summarizeContextPreview(userContexts);

    exchanges.push({
      id: exchangeID,
      index: exchanges.length + 1,
      messageIDs,
      createdAt,
      modelID: modelID || undefined,
      userText,
      userPreview: makePreviewLine(userText, makePreviewLine(fallbackUserPreview, "")),
      userContexts,
      ...(userCollab ? { userCollab } : {}),
      assistantText,
      assistantPreview: makePreviewLine(assistantText, thinkingText ? makePreviewLine(thinkingText, "") : ""),
      thinkingText,
      tools,
      images: exchangeImages,
      redactionCount: totalRedactions - redactionsBefore,
    });
  }

  return { exchanges, allImages, redactionCount: totalRedactions };
}

export function resolveInitialExchangeSelection(
  exchanges: ShareExchange[],
  focusMessageID?: string
): Set<string> {
  if (exchanges.length === 0) return new Set<string>();
  if (focusMessageID) {
    const matched = exchanges.find((ex) => ex.messageIDs.includes(focusMessageID));
    if (matched) return new Set([matched.id]);
  }
  return new Set(exchanges.map((ex) => ex.id));
}

export function buildShareDocument(
  session: SessionInfo | undefined,
  sessionID: string,
  turns: ShareTurnInput[],
  options: ShareBuildOptions
): ShareDocument {
  const workDir = options.workspaceDir || session?.directory || "";
  const grouped = groupTurnsIntoExchanges(turns, {
    hideLocalPaths: options.hideLocalPaths,
    redactSecrets: options.redactSecrets,
    workspaceDir: workDir,
  });

  const filteredExchanges = grouped.exchanges
    .filter((ex) => !options.selectedExchangeIDs || options.selectedExchangeIDs.has(ex.id))
    .map((ex) => ({
      ...ex,
      tools: options.includeTools ? ex.tools : [],
      thinkingText: options.includeThinking ? ex.thinkingText : "",
      images: options.includeImages
        ? ex.images.filter((img) => !options.disabledImageIDs?.has(img.id))
        : [],
    }));

  const rawTitle = (session?.title || "").trim() || sessionID;
  const titleRedaction = options.redactSecrets
    ? redactShareSecrets(rawTitle)
    : { text: rawTitle, count: 0 };
  const cleanTitle = titleRedaction.text;
  const selectedRedactions =
    titleRedaction.count +
    filteredExchanges.reduce((sum, ex) => sum + ex.redactionCount, 0);
  const lastModel =
    [...filteredExchanges].reverse().find((ex) => ex.modelID)?.modelID ||
    session?.modelID ||
    "";

  const projectLabel = workDir ? basenameOf(workDir.replace(/\/\.hoxi\/worktrees\/.*$/, "")) : "";

  return {
    sessionID,
    title: cleanTitle,
    runtime: session?.runtime || "native",
    modelID: lastModel,
    projectLabel,
    createdAt: session?.time?.created,
    updatedAt: session?.time?.updated,
    exchanges: filteredExchanges,
    allImages: grouped.allImages,
    redactionCount: selectedRedactions,
  };
}
