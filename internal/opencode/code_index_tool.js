import { tool } from "./plugin_tool.js";
import { isProtectedPath, isSecretPath, isSensitiveExternalPath } from "../../web/src/policy/permission-policy.js";

const indexActions = ["map", "context", "search", "inspect", "callers", "callees", "impact", "refresh"];

function isSensitiveIndexPath(rawPath) {
  if (typeof rawPath !== "string") return false;
  const trimmed = rawPath.trim();
  if (!trimmed) return false;
  const parts = trimmed.split(/[\\/]+/).filter(Boolean);
  if (parts.includes("..")) return true;
  return isProtectedPath(trimmed) || isSecretPath(trimmed) || isSensitiveExternalPath(trimmed);
}

export function formatCodeIndexOutput(action, payload) {
  if (!payload || typeof payload !== "object") return JSON.stringify(payload);
  if (action === "map" && Array.isArray(payload.entries)) {
    const entries = payload.entries;
    if (entries.length === 0) return "No indexed code files found for map.";
    const hdr = payload.pathPrefix
      ? `Repository Backbone Map [${payload.pathPrefix}] (${entries.length} files):`
      : `Repository Backbone Map (${entries.length} top of ${payload.files || 0} code files):`;
    const lines = [hdr];
    for (const e of entries) {
      lines.push(`${e.path || ""} (rank ${e.score || 0})`);
      const syms = Array.isArray(e.symbols) ? e.symbols : [];
      for (const sym of syms) {
        const refNote = sym.references > 0 ? ` [${sym.references} refs]` : "";
        const sig = sym.signature || sym.symbol || "";
        lines.push(`  L${sym.startLine || 0}-${sym.endLine || 0}${refNote}: ${sig}`);
      }
    }
    return lines.join("\n");
  }
  if (action === "inspect" && typeof payload.content === "string" && typeof payload.path === "string") {
    const sym = payload.symbol ? ` [${payload.symbol}]` : "";
    const trunc = payload.truncated ? " [truncated]" : "";
    return `# ${payload.path}:${payload.startLine}-${payload.endLine} (of ${payload.totalLines})${sym}${trunc}\n${payload.content}`;
  }
  if (action === "search" && Array.isArray(payload.results)) {
    if (payload.results.length === 0) return "No matches found.";
    if (typeof payload.results[0]?.path === "string") {
      return payload.results
        .map((r) => {
          const sym = r.symbol ? ` [${r.symbol}]` : "";
          const snip =
            typeof r.snippet === "string" && r.snippet.trim()
              ? `\n  ${r.snippet.trim().replace(/\n+/g, "\n  ")}`
              : "";
          return `${r.path}:${r.startLine}-${r.endLine}${sym} (score ${r.score})${snip}`;
        })
        .join("\n");
    }
  }
  if (action === "callers" && (Array.isArray(payload.callers) || Array.isArray(payload.tests))) {
    const callers = Array.isArray(payload.callers) ? payload.callers : [];
    const tests = Array.isArray(payload.tests) ? payload.tests : [];
    const fmtHit = (h) => `  ${h.path}:${h.line}${h.symbol ? ` [${h.symbol}]` : ""} — ${h.snippet || ""}`;
    return [
      `Callers (${callers.length}):`,
      ...(callers.length ? callers.map(fmtHit) : ["  (none)"]),
      `Tests (${tests.length}):`,
      ...(tests.length ? tests.map(fmtHit) : ["  (none)"]),
    ].join("\n");
  }
  if (action === "callees" && Array.isArray(payload.callees)) {
    const callees = payload.callees;
    const imports =
      Array.isArray(payload.imports) && payload.imports.length ? `Imports: ${payload.imports.join(", ")}\n` : "";
    const fmtCallee = (c) => `  ${c.path}:${c.startLine}-${c.endLine} [${c.symbol}] (called at L${c.callLine})`;
    return `${imports}Callees (${callees.length}):\n${callees.length ? callees.map(fmtCallee).join("\n") : "  (none)"}`;
  }
  if (action === "impact" && (Array.isArray(payload.directCallers) || Array.isArray(payload.tests))) {
    const direct = Array.isArray(payload.directCallers) ? payload.directCallers : [];
    const trans = Array.isArray(payload.transitiveCallers) ? payload.transitiveCallers : [];
    const tests = Array.isArray(payload.tests) ? payload.tests : [];
    const fmtHit = (h) => `  ${h.path}:${h.line}${h.symbol ? ` [${h.symbol}]` : ""} — ${h.snippet || ""}`;
    const fmtTrans = (h) =>
      `  ${h.path}:${h.line}${h.symbol ? ` [${h.symbol}]` : ""} (depth ${h.depth}, via ${h.via}) — ${h.snippet || ""}`;
    return [
      `Direct Callers (${direct.length}):`,
      ...(direct.length ? direct.map(fmtHit) : ["  (none)"]),
      ...(trans.length ? [`Transitive Callers (${trans.length}):`, ...trans.map(fmtTrans)] : []),
      `Related Tests (${tests.length}):`,
      ...(tests.length ? tests.map(fmtHit) : ["  (none)"]),
    ].join("\n");
  }
  if (action === "context" && payload.inspect && typeof payload.inspect.content === "string") {
    const insp = payload.inspect;
    const sym = insp.symbol ? ` [${insp.symbol}]` : "";
    const trunc = insp.truncated ? " [truncated]" : "";
    const callees = Array.isArray(payload.callees) ? payload.callees : [];
    const callers = Array.isArray(payload.callers) ? payload.callers : [];
    const trans = Array.isArray(payload.transitiveCallers) ? payload.transitiveCallers : [];
    const tests = Array.isArray(payload.tests) ? payload.tests : [];
    const fmtCallee = (c) => `  ${c.path}:${c.startLine}-${c.endLine} [${c.symbol}] (called at L${c.callLine})`;
    const fmtHit = (h) => `  ${h.path}:${h.line}${h.symbol ? ` [${h.symbol}]` : ""} — ${h.snippet || ""}`;
    const fmtTrans = (h) => `  ${h.path}:${h.line}${h.symbol ? ` [${h.symbol}]` : ""} (depth ${h.depth}, via ${h.via})`;
    return [
      `# ${insp.path}:${insp.startLine}-${insp.endLine} (of ${insp.totalLines})${sym}${trunc}`,
      insp.content,
      `Callees (${callees.length}):`,
      ...(callees.length ? callees.map(fmtCallee) : ["  (none)"]),
      `Callers (${callees.length}):`,
      ...(callers.length ? callers.map(fmtHit) : ["  (none)"]),
      ...(trans.length ? [`Transitive Callers (${trans.length}):`, ...trans.map(fmtTrans)] : []),
      `Tests (${tests.length}):`,
      ...(tests.length ? tests.map(fmtHit) : ["  (none)"]),
    ].join("\n");
  }
  if (action === "refresh" && typeof payload.files === "number") {
    return `Index refreshed (${payload.files} files, ${payload.chunks || 0} chunks).`;
  }
  return JSON.stringify(payload);
}

export function createCodeIndexTool() {
  const indexArgs = {
    action: tool.schema
      .enum(indexActions)
      .describe("map | context | search | inspect | callers | callees | impact | refresh"),
    query: tool.schema.string().optional().describe("Symbol or keyword query for map focus, search, or context"),
    symbol: tool.schema.string().optional().describe("Symbol name for context, inspect, callers, callees, or impact"),
    path: tool.schema
      .string()
      .optional()
      .describe("Relative file path (context/inspect/callers/callees/impact) or directory prefix (map/search)"),
    startLine: tool.schema.number().finite().optional().describe("1-based start line for inspect"),
    endLine: tool.schema.number().finite().optional().describe("1-based end line for inspect"),
    contextLines: tool.schema
      .number()
      .finite()
      .optional()
      .describe("Extra context lines for inspect or context (default 5, max 30)"),
    depth: tool.schema.number().finite().optional().describe("Call graph depth for impact (default 2, max 3)"),
    limit: tool.schema
      .number()
      .finite()
      .optional()
      .describe("Max files/hits for map, search, callers, callees, or impact (default 12-16)"),
    refresh: tool.schema.boolean().optional().describe("Refresh worktree index before map or search"),
  };
  const indexSchema = tool.schema.object(indexArgs);

  return tool({
    description:
      "Navigate workspace code efficiently via the worktree-scoped knowledge graph. Use action='map' first for a PageRank-ordered architectural overview of hub files and key symbols, action='context' to fetch a symbol's source, callees, callers, blast radius, and tests in 1 call, or search -> inspect -> callers/callees/impact -> refresh.",
    args: indexArgs,
    async execute(input, context) {
      const req = indexSchema.parse(input);
      if (!context.directory) throw new Error("Code index workspace directory scope is required");
      const rawBase = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
      if (!rawBase) throw new Error("Hoxi gateway URL is unavailable");
      context.abort.throwIfAborted();
      if (req.path && isSensitiveIndexPath(req.path)) {
        if (typeof context.ask === "function") {
          await context.ask({
            permission: "read",
            patterns: [req.path],
            always: [],
            metadata: {
              tool: "hoxi_code_index",
              ...req,
              filepath: req.path,
              sessionID: context.sessionID,
              directory: context.directory,
            },
          });
        }
        context.abort.throwIfAborted();
        throw new Error(`Refusing to inspect protected or secret path: ${req.path}`);
      }
      const base = rawBase.replace(/\/$/, "");
      let url;
      let method = "GET";
      let body;
      if (req.action === "refresh") {
        url = new URL(base + "/api/hoxi/code-index/refresh");
        url.searchParams.set("directory", context.directory);
        method = "POST";
        body = JSON.stringify({ directory: context.directory });
      } else if (req.action === "map") {
        url = new URL(base + "/api/hoxi/code-index/map");
        url.searchParams.set("directory", context.directory);
        if (req.path) url.searchParams.set("path", req.path);
        const q = req.query || req.symbol || "";
        if (q) url.searchParams.set("q", q);
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
        if (req.refresh) url.searchParams.set("refresh", "1");
      } else if (req.action === "inspect") {
        url = new URL(base + "/api/hoxi/code-index/inspect");
        url.searchParams.set("directory", context.directory);
        if (req.path) url.searchParams.set("path", req.path);
        if (req.symbol) url.searchParams.set("symbol", req.symbol);
        if (req.startLine) url.searchParams.set("startLine", String(Math.trunc(req.startLine)));
        if (req.endLine) url.searchParams.set("endLine", String(Math.trunc(req.endLine)));
        if (req.contextLines !== undefined) url.searchParams.set("contextLines", String(Math.trunc(req.contextLines)));
      } else if (req.action === "callers") {
        url = new URL(base + "/api/hoxi/code-index/callers");
        url.searchParams.set("directory", context.directory);
        if (req.symbol) url.searchParams.set("symbol", req.symbol);
        if (req.path) url.searchParams.set("path", req.path);
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
      } else if (req.action === "callees") {
        url = new URL(base + "/api/hoxi/code-index/callees");
        url.searchParams.set("directory", context.directory);
        if (req.symbol) url.searchParams.set("symbol", req.symbol);
        if (req.path) url.searchParams.set("path", req.path);
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
      } else if (req.action === "impact") {
        url = new URL(base + "/api/hoxi/code-index/impact");
        url.searchParams.set("directory", context.directory);
        if (req.symbol) url.searchParams.set("symbol", req.symbol);
        if (req.path) url.searchParams.set("path", req.path);
        if (req.depth) url.searchParams.set("depth", String(Math.trunc(req.depth)));
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
      } else if (req.action === "context") {
        url = new URL(base + "/api/hoxi/code-index/context");
        url.searchParams.set("directory", context.directory);
        if (req.symbol) url.searchParams.set("symbol", req.symbol);
        if (req.query) url.searchParams.set("q", req.query);
        if (req.path) url.searchParams.set("path", req.path);
        if (req.contextLines !== undefined) url.searchParams.set("contextLines", String(Math.trunc(req.contextLines)));
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
      } else {
        url = new URL(base + "/api/hoxi/code-index/search");
        url.searchParams.set("directory", context.directory);
        url.searchParams.set("q", req.query || req.symbol || "");
        if (req.path) url.searchParams.set("path", req.path);
        if (req.limit) url.searchParams.set("limit", String(Math.trunc(req.limit)));
        if (req.refresh) url.searchParams.set("refresh", "1");
      }
      const response = await fetch(url, {
        method,
        headers: body ? { "Content-Type": "application/json" } : undefined,
        body,
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) throw new Error(`Hoxi code index HTTP ${response.status}: ${await response.text()}`);
      const payload = await response.json();
      return {
        title: `CodeIndex: ${req.action}`,
        output: formatCodeIndexOutput(req.action, payload),
      };
    },
  });
}
