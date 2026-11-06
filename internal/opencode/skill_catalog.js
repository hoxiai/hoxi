// Narrows OpenCode's per-request skill catalog to what fits the session.
// Hoxi decides each skill's exposure once (GET /api/hoxi/skills/list, the same
// data the skills panel shows): "auto" keeps the upstream entry, "on-demand"
// keeps only the name, and "unavailable" is hidden and refused at load time.

const CATALOG_HEADER = "Skills provide specialized instructions and workflows for specific tasks.";
const OPEN_TAG = "<available_skills>";
const CLOSE_TAG = "</available_skills>";
const FETCH_TIMEOUT_MS = 4000;
const FAILURE_RETRY_MS = 30_000;

/**
 * Rewrites the <available_skills> block inside text. exposures maps a skill
 * name to { exposure, reason }. Skills Hoxi has no decision for stay listed.
 * Returns null when text has no catalog block.
 */
export function rewriteSkillCatalog(text, exposures) {
  const span = catalogSpan(text);
  if (!span) return null;
  const { open, close } = span;
  const lines = text.slice(open + OPEN_TAG.length, close).split("\n");
  const kept = [];
  const onDemand = [];
  let entry = null;
  for (const line of lines) {
    if (line.trim() === "<skill>") {
      entry = { lines: [line], name: "" };
      continue;
    }
    if (!entry) {
      kept.push(line);
      continue;
    }
    entry.lines.push(line);
    const name = /^\s*<name>(.*)<\/name>\s*$/.exec(line);
    if (name) entry.name = name[1];
    if (line.trim() !== "</skill>") continue;
    const exposure = exposures.get(entry.name)?.exposure;
    if (exposure === "on-demand") onDemand.push(entry.name);
    else if (exposure !== "unavailable") kept.push(...entry.lines);
    entry = null;
  }
  if (entry) kept.push(...entry.lines);
  let block = OPEN_TAG + kept.join("\n") + CLOSE_TAG;
  if (onDemand.length > 0) {
    block +=
      `\nOn-demand skills, listed by name only: ${onDemand.join(", ")}. ` +
      "Load one with the skill tool only when the user names it or asks for its specific workflow.";
  }
  return text.slice(0, open) + block + text.slice(close + CLOSE_TAG.length);
}

function catalogSpan(text) {
  if (typeof text !== "string") return null;
  const start = text.indexOf(CATALOG_HEADER);
  if (start < 0) return null;
  const open = text.indexOf(OPEN_TAG, start);
  const close = open < 0 ? -1 : text.indexOf(CLOSE_TAG, open);
  if (open < 0 || close < 0) return null;
  return { start, open, close };
}

function unavailableReason(item) {
  if (item.exposureReason === "missing-tools" && Array.isArray(item.missingTools) && item.missingTools.length) {
    return `it needs tools this runtime does not provide (${item.missingTools.join(", ")})`;
  }
  if (item.exposureReason === "config-denied") return "it is disabled by permission.skill in the engine configuration";
  return "it is unavailable in this runtime";
}

/**
 * One instance per OpenCode project instance. It fetches Hoxi's exposure
 * decisions when the upstream catalog block changes and reuses them, so the
 * rewritten prompt stays byte-identical across turns for prompt caching.
 */
export function createSkillCatalog(factoryDir) {
  let decidedBlock = null;
  let exposures = new Map();
  let retryAfter = 0;

  async function fetchExposures() {
    const base = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
    if (!base || !factoryDir) return null;
    const url = new URL(base.replace(/\/$/, "") + "/api/hoxi/skills/list");
    url.searchParams.set("directory", factoryDir);
    url.searchParams.set("runtime", "opencode");
    const response = await fetch(url, { redirect: "error", signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) });
    if (!response.ok) return null;
    const body = await response.json();
    if (!Array.isArray(body?.skills)) return null;
    const map = new Map();
    for (const item of body.skills) {
      if (item && typeof item.name === "string" && typeof item.exposure === "string") map.set(item.name, item);
    }
    return map;
  }

  return {
    /** Rewrites the catalog in output.system in place; failures keep it unchanged. */
    async apply(system) {
      if (!Array.isArray(system)) return;
      const index = system.findIndex((s) => typeof s === "string" && s.includes(CATALOG_HEADER));
      if (index < 0) return;
      const text = system[index];
      const span = catalogSpan(text);
      if (!span) return;
      // Decisions follow the upstream catalog: the engine rescans skills only
      // when its instance reloads, which changes this block.
      const block = text.slice(span.start, span.close);
      if (block !== decidedBlock) {
        if (Date.now() < retryAfter) return;
        let fresh = null;
        try {
          fresh = await fetchExposures();
        } catch {
          fresh = null;
        }
        if (!fresh) {
          retryAfter = Date.now() + FAILURE_RETRY_MS;
          return;
        }
        exposures = fresh;
        decidedBlock = block;
      }
      const rewritten = rewriteSkillCatalog(text, exposures);
      if (rewritten) system[index] = rewritten;
    },
    /** Refuses loading a skill Hoxi marked unavailable. */
    guard(input, output) {
      if (input?.tool !== "skill") return;
      const name = typeof output?.args?.name === "string" ? output.args.name.trim() : "";
      const item = exposures.get(name);
      if (item?.exposure !== "unavailable") return;
      throw new Error(
        `Skill "${name}" cannot be loaded: ${unavailableReason(item)}. Continue without it and tell the user if the task needs it.`,
      );
    },
  };
}
