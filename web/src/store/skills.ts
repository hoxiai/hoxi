// web/src/store/skills.ts
import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import {
  skillsAPI,
  type MarketplaceSkill,
  type MessageInfo,
  type MessagePart,
  type SkillDescriptor,
  type SkillDetail,
  type SkillListResponse,
  type SkillSelection,
} from "../services/api";
import { setWorkspace, workspace } from "./workspace-state";
import { sessionDirectory, sessionRuntimeForID } from "./session-query";
import { normalizeFolderPath } from "./workspace-folders";

export const HIDDEN_SKILLS_STORAGE_KEY = "hoxi.skills.hidden:v1";

export type TurnSkillLoadStatus =
  | "requested"
  | "loading"
  | "loaded"
  | "error"
  | "unconfirmed";

interface SkillsStoreState {
  skills: SkillDescriptor[];
  supported: boolean;
  runtime: string;
  directory: string;
  cacheHint: string;
  loading: boolean;
  error: string | null;
  selectedKey: string | null;
  detail: SkillDetail | null;
  detailLoading: boolean;
  detailError: string | null;
  selectedBySession: Record<string, SkillSelection | null>;
  noticeBySession: Record<string, string | null>;
  marketplace: MarketplaceSkill[];
  marketplaceSource: string;
  marketplaceHint: string;
  marketplaceLoading: boolean;
  marketplaceError: string | null;
  installingSlug: string | null;
  installNotice: string | null;
}

function loadHiddenSkills(): string[] {
  try {
    const raw = localStorage.getItem(HIDDEN_SKILLS_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return parsed.filter((item): item is string => typeof item === "string" && item.trim().length > 0);
    }
  } catch {
    /* ignore */
  }
  return [];
}

const [hiddenSkillNames, setHiddenSkillNames] = createSignal<string[]>(loadHiddenSkills());
export { hiddenSkillNames };

export function isSkillHidden(name: string): boolean {
  return hiddenSkillNames().includes(name.trim());
}

export function toggleSkillHidden(name: string) {
  const clean = name.trim();
  if (!clean) return;
  setHiddenSkillNames((prev) => {
    const next = prev.includes(clean)
      ? prev.filter((n) => n !== clean)
      : [...prev, clean];
    try {
      localStorage.setItem(HIDDEN_SKILLS_STORAGE_KEY, JSON.stringify(next));
    } catch {
      /* ignore */
    }
    return next;
  });
}

export const [skillsStore, setSkillsStore] = createStore<SkillsStoreState>({
  skills: [],
  supported: true,
  runtime: "opencode",
  directory: "",
  cacheHint: "",
  loading: false,
  error: null,
  selectedKey: null,
  detail: null,
  detailLoading: false,
  detailError: null,
  selectedBySession: {},
  noticeBySession: {},
  marketplace: [],
  marketplaceSource: "",
  marketplaceHint: "",
  marketplaceLoading: false,
  marketplaceError: null,
  installingSlug: null,
  installNotice: null,
});

const catalogCache = new Map<string, { response: SkillListResponse; expiresAt: number }>();
const detailCache = new Map<string, { detail: SkillDetail; expiresAt: number }>();
const descriptorBySelectionKey = new Map<string, SkillDescriptor>();

const CATALOG_CACHE_TTL_MS = 20_000;
let listRequestGen = 0;
let detailRequestGen = 0;

export function buildSkillsCacheKey(
  runtime: string,
  version: string,
  directory: string,
  sessionID?: string | null
): string {
  return `${runtime || "opencode"}\x00${version || "0"}\x00${directory || "__root__"}\x00${sessionID || "__workspace__"}`;
}

export function invalidateSkillsCatalogCache() {
  listRequestGen++;
  detailRequestGen++;
  catalogCache.clear();
  detailCache.clear();
}

function sessionKey(sessionID?: string | null, directory?: string | null): string {
  if (sessionID) return sessionID;
  const dir = normalizeFolderPath(directory ?? workspace.rootDir);
  return dir ? `__new__:${dir}` : "__new__";
}

export function getComposerSkill(
  sessionID?: string | null,
  directory?: string | null
): SkillSelection | null {
  return skillsStore.selectedBySession[sessionKey(sessionID, directory)] ?? null;
}

export function selectComposerSkill(
  sessionID: string | null | undefined,
  selection: SkillSelection | null,
  descriptor?: SkillDescriptor,
  directory?: string | null
) {
  const key = sessionKey(sessionID, directory);
  if (selection && descriptor) {
    descriptorBySelectionKey.set(selection.key, descriptor);
  }
  setSkillsStore("selectedBySession", key, selection ? { key: selection.key, name: selection.name } : null);
  if (selection) {
    setSkillsStore("noticeBySession", key, null);
  }
}

export function transferComposerSkillSelection(
  fromSessionID: string | null | undefined,
  toSessionID: string,
  updatedSelection?: SkillSelection | null,
  directory?: string | null
) {
  const fromKey = sessionKey(fromSessionID, directory);
  const current = updatedSelection !== undefined ? updatedSelection : skillsStore.selectedBySession[fromKey];
  setSkillsStore("selectedBySession", fromKey, null);
  setSkillsStore("selectedBySession", toSessionID, current ? { ...current } : null);
}

export function transferDraftComposerSkill(toSessionID: string, directory?: string | null) {
  transferComposerSkillSelection(null, toSessionID, undefined, directory);
}

export function getComposerSkillNotice(
  sessionID?: string | null,
  directory?: string | null
): string | null {
  return skillsStore.noticeBySession[sessionKey(sessionID, directory)] ?? null;
}

export function setComposerSkillNotice(
  sessionID: string | null | undefined,
  notice: string | null,
  directory?: string | null
) {
  setSkillsStore("noticeBySession", sessionKey(sessionID, directory), notice);
}

export function recordSkillDescriptors(list: SkillDescriptor[]) {
  for (const item of list) {
    descriptorBySelectionKey.set(item.key, item);
  }
}

export async function loadSkillsCatalog(options?: {
  directory?: string;
  sessionID?: string | null;
  runtime?: string;
  forceRefresh?: boolean;
}): Promise<SkillListResponse | null> {
  const sid = options?.sessionID !== undefined ? options.sessionID : workspace.activeSessionID;
  const dir = options?.directory ?? (sessionDirectory(sid) || workspace.rootDir || "");
  const rt = options?.runtime ?? sessionRuntimeForID(sid);
  const version = workspace.appVersion || "0";

  if (!dir) {
    setSkillsStore({
      skills: [],
      supported: true,
      runtime: rt,
      directory: "",
      loading: false,
      error: null,
    });
    return null;
  }

  const cKey = buildSkillsCacheKey(rt, version, dir, sid);
  if (!options?.forceRefresh) {
    const cached = catalogCache.get(cKey);
    if (cached && Date.now() <= cached.expiresAt) {
      recordSkillDescriptors(cached.response.skills ?? []);
      setSkillsStore({
        skills: cached.response.skills ?? [],
        supported: cached.response.supported,
        runtime: cached.response.runtime || rt,
        directory: cached.response.directory || dir,
        cacheHint: cached.response.cacheHint || "",
        loading: false,
        error: null,
      });
      return cached.response;
    }
  }

  const reqGen = ++listRequestGen;
  setSkillsStore({ loading: true, error: null, runtime: rt, directory: dir });

  try {
    const resp = await skillsAPI.fetchSkillsList({
      directory: dir,
      sessionID: sid,
      runtime: rt,
    });
    if (reqGen !== listRequestGen) return null;
    const activeSid = options?.sessionID !== undefined ? options.sessionID : workspace.activeSessionID;
    const activeDir = options?.directory ?? (sessionDirectory(activeSid) || workspace.rootDir || "");
    if (activeSid !== sid || activeDir !== dir) return null;

    const list = Array.isArray(resp?.skills) ? resp.skills : [];
    recordSkillDescriptors(list);
    catalogCache.set(cKey, {
      response: { ...resp, skills: list },
      expiresAt: Date.now() + CATALOG_CACHE_TTL_MS,
    });
    setSkillsStore({
      skills: list,
      supported: Boolean(resp?.supported),
      runtime: resp?.runtime || rt,
      directory: resp?.directory || dir,
      cacheHint: resp?.cacheHint || "",
      loading: false,
      error: null,
    });
    if (skillsStore.selectedKey && !list.some((s) => s.key === skillsStore.selectedKey)) {
      setSkillsStore({ selectedKey: list[0]?.key ?? null, detail: null });
    }
    return resp;
  } catch (err) {
    if (reqGen !== listRequestGen) return null;
    const msg = err instanceof Error ? err.message : String(err || "Failed to load skills");
    setSkillsStore({ loading: false, error: msg, skills: [] });
    return null;
  }
}

export async function loadSkillDetail(
  key: string,
  options?: {
    directory?: string;
    sessionID?: string | null;
    runtime?: string;
    forceRefresh?: boolean;
  }
): Promise<SkillDetail | null> {
  const trimmedKey = (key || "").trim();
  if (!trimmedKey) {
    setSkillsStore({ selectedKey: null, detail: null, detailLoading: false, detailError: null });
    return null;
  }
  const sid = options?.sessionID !== undefined ? options.sessionID : workspace.activeSessionID;
  const dir = options?.directory ?? (sessionDirectory(sid) || workspace.rootDir || "");
  const rt = options?.runtime ?? sessionRuntimeForID(sid);
  const version = workspace.appVersion || "0";
  const dKey = `${buildSkillsCacheKey(rt, version, dir, sid)}\x00${trimmedKey}`;

  setSkillsStore("selectedKey", trimmedKey);
  if (!options?.forceRefresh) {
    const cached = detailCache.get(dKey);
    if (cached && Date.now() <= cached.expiresAt) {
      setSkillsStore({ detail: cached.detail, detailLoading: false, detailError: null });
      return cached.detail;
    }
  }

  const reqGen = ++detailRequestGen;
  setSkillsStore({ detailLoading: true, detailError: null });
  try {
    const detail = await skillsAPI.fetchSkillDetail({
      key: trimmedKey,
      directory: dir,
      sessionID: sid,
      runtime: rt,
    });
    if (reqGen !== detailRequestGen) return null;
    detailCache.set(dKey, {
      detail,
      expiresAt: Date.now() + CATALOG_CACHE_TTL_MS,
    });
    descriptorBySelectionKey.set(detail.key, detail);
    setSkillsStore({ detail, detailLoading: false, detailError: null });
    return detail;
  } catch (err) {
    if (reqGen !== detailRequestGen) return null;
    const msg = err instanceof Error ? err.message : String(err || "Failed to load skill detail");
    setSkillsStore({ detail: null, detailLoading: false, detailError: msg });
    return null;
  }
}

let marketplaceRequestGen = 0;

export async function loadSkillMarketplace(options?: {
  forceRefresh?: boolean;
}): Promise<MarketplaceSkill[] | null> {
  const reqGen = ++marketplaceRequestGen;
  setSkillsStore({ marketplaceLoading: true, marketplaceError: null });
  try {
    const resp = await skillsAPI.fetchMarketplaceSkills({
      refresh: Boolean(options?.forceRefresh),
    });
    if (reqGen !== marketplaceRequestGen) return null;
    const list = Array.isArray(resp?.skills) ? resp.skills : [];
    setSkillsStore({
      marketplace: list,
      marketplaceSource: resp?.source || "",
      marketplaceHint: resp?.cacheHint || "",
      marketplaceLoading: false,
      marketplaceError: null,
    });
    return list;
  } catch (err) {
    if (reqGen !== marketplaceRequestGen) return null;
    const msg = err instanceof Error ? err.message : String(err || "Failed to load skills marketplace");
    setSkillsStore({ marketplaceLoading: false, marketplaceError: msg, marketplace: [] });
    return null;
  }
}

export async function installSkillFromMarketplace(
  slug: string,
  options?: { overwrite?: boolean }
): Promise<boolean> {
  const clean = (slug || "").trim();
  if (!clean) return false;
  setSkillsStore({ installingSlug: clean, installNotice: null });
  try {
    const resp = await skillsAPI.installMarketplaceSkill({
      slug: clean,
      overwrite: Boolean(options?.overwrite),
    });
    const installed = resp?.skill;
    const nextMarketplace = skillsStore.marketplace.map((item) =>
      item.slug === clean
        ? {
            ...item,
            installed: true,
            installedDigest: installed?.installedDigest || item.contentSha256,
            updateAvailable: false,
          }
        : item
    );
    setSkillsStore("marketplace", nextMarketplace);
    invalidateSkillsCatalogCache();
    void loadSkillsCatalog({ forceRefresh: true });
    setSkillsStore({
      installingSlug: null,
      installNotice: t("skills.installSuccess", { name: installed?.name || clean }),
    });
    return true;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err || "");
    setSkillsStore({
      installingSlug: null,
      installNotice: msg
        ? t("skills.installFailed", { message: msg })
        : t("skills.installFailed", { message: clean }),
    });
    return false;
  }
}

export function clearSkillInstallNotice() {
  setSkillsStore("installNotice", null);
}

export function openSkillInInspector(key?: string) {
  setWorkspace({
    inspectorOpen: true,
    inspectorTab: "skills",
  });
  if (key) {
    void loadSkillDetail(key);
  }
}

/**
 * Validates a selected skill against the target session's effective directory and runtime.
 * If a brand-new sandbox worktree was just provisioned from the main workspace, updates `key`
 * ONLY when the skill exists in the new worktree with identical source and contentDigest.
 */
export async function revalidateSkillForTargetDirectory(params: {
  sessionID: string;
  directory: string;
  runtime: string;
  selection: SkillSelection;
}): Promise<{ ok: true; selection: SkillSelection } | { ok: false; reason: string }> {
  const { sessionID, directory, runtime, selection } = params;
  try {
    const resp = await skillsAPI.fetchSkillsList({
      directory,
      sessionID,
      runtime,
    });
    if (!resp.supported) {
      return {
        ok: false,
        reason: t("skills.unsupportedTitle", { runtime: resp.runtime || runtime }),
      };
    }
    const list = resp.skills ?? [];
    recordSkillDescriptors(list);

    const exact = list.find((s) => s.key === selection.key && s.name === selection.name);
    if (exact) {
      return { ok: true, selection: { key: exact.key, name: exact.name } };
    }

    const byName = list.find((s) => s.name === selection.name);
    const orig = descriptorBySelectionKey.get(selection.key);
    if (
      byName &&
      orig &&
      byName.source === orig.source &&
      (!orig.contentDigest || byName.contentDigest === orig.contentDigest)
    ) {
      return { ok: true, selection: { key: byName.key, name: byName.name } };
    }

    return {
      ok: false,
      reason: t("skills.reselectRequired", { name: selection.name }),
    };
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err || "");
    return {
      ok: false,
      reason: msg || t("skills.reselectRequired", { name: selection.name }),
    };
  }
}

export function extractUserTurnSkillSelection(parts: MessagePart[]): SkillSelection | null {
  for (const part of parts ?? []) {
    if (part.type !== "text") continue;
    const raw = part.metadata?.hoxiSkill;
    if (
      raw &&
      typeof raw === "object" &&
      typeof raw.key === "string" &&
      raw.key.trim() &&
      typeof raw.name === "string" &&
      raw.name.trim()
    ) {
      return { key: raw.key.trim(), name: raw.name.trim() };
    }
  }
  return null;
}

/**
 * Resolves the authentic skill loading status for a user turn from native `skill` tool parts
 * in the corresponding assistant execution cycle (Section 7 of docs/skills-development.md).
 */
export function resolveTurnSkillStatus(
  turns: Array<{ info: MessageInfo; parts: MessagePart[] }>,
  userTurnIndex: number,
  isBusy: boolean
): { skill: SkillSelection; status: TurnSkillLoadStatus } | null {
  const userTurn = turns[userTurnIndex];
  if (!userTurn || userTurn.info.role !== "user") return null;
  const skill = extractUserTurnSkillSelection(userTurn.parts);
  if (!skill) return null;
  // CLI runtimes receive the SKILL.md body inline with the prompt, so the
  // selection is already delivered even though no native `skill` tool runs.
  for (const part of userTurn.parts ?? []) {
    if (part.type !== "text") continue;
    const raw = part.metadata?.hoxiSkill;
    if (raw && typeof raw === "object" && raw.mode === "inline") {
      return { skill, status: "loaded" };
    }
  }

  let isLatestCycle = true;
  let hasCompletedMatch = false;
  let hasRunningMatch = false;
  let hasErrorMatch = false;

  for (let i = userTurnIndex + 1; i < turns.length; i++) {
    const turn = turns[i];
    if (turn.info.role === "user") {
      isLatestCycle = false;
      break;
    }
    for (const part of turn.parts ?? []) {
      if (part.type !== "tool" || (part.tool || "").toLowerCase() !== "skill") continue;
      const inputName =
        typeof part.state.input?.name === "string" ? part.state.input.name.trim() : "";
      const metaName =
        typeof part.state.metadata?.name === "string" ? part.state.metadata.name.trim() : "";
      const targetName = inputName || metaName;
      if (targetName !== skill.name) continue;
      if (inputName && metaName && inputName !== metaName) continue;

      if (part.state.status === "completed") {
        hasCompletedMatch = true;
      } else if (part.state.status === "pending" || part.state.status === "running") {
        hasRunningMatch = true;
      } else if (part.state.status === "error") {
        hasErrorMatch = true;
      }
    }
  }

  const cycleActive = isLatestCycle && isBusy;
  if (hasCompletedMatch) {
    return { skill, status: "loaded" };
  }
  if (hasRunningMatch && cycleActive) {
    return { skill, status: "loading" };
  }
  if (hasErrorMatch) {
    return { skill, status: "error" };
  }
  if (cycleActive) {
    return { skill, status: "requested" };
  }
  return { skill, status: "unconfirmed" };
}
