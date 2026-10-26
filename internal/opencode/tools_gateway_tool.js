import { tool } from "@opencode-ai/plugin";

const toolsActions = ["discover", "inspect", "run"];

export function createToolsGatewayTool() {
  const toolsArgs = {
    action: tool.schema.enum(toolsActions).describe("discover | inspect | run"),
    query: tool.schema
      .string()
      .optional()
      .describe("Natural-language search query for discover (e.g. 'amazon reviews', 'ahrefs backlinks', '小红书达人')"),
    category: tool.schema.string().optional().describe("Optional category slug for discover"),
    limit: tool.schema.number().finite().optional().describe("Max results for discover (default 8, max 30)"),
    provider: tool.schema
      .string()
      .optional()
      .describe("Provider slug for inspect or run (e.g. 'apify', 'dataforseo', 'ahrefs')"),
    endpoint: tool.schema
      .string()
      .optional()
      .describe("Endpoint path/id for inspect or run (e.g. '/v3/merchant/amazon/products/live')"),
    body: tool.schema
      .record(tool.schema.string(), tool.schema.unknown())
      .optional()
      .describe("JSON request body for run"),
    queryParams: tool.schema
      .record(tool.schema.string(), tool.schema.unknown())
      .optional()
      .describe("Optional URL query parameters for run"),
    pathParams: tool.schema
      .record(tool.schema.string(), tool.schema.unknown())
      .optional()
      .describe("Optional path parameters for run"),
    wait: tool.schema.boolean().optional().describe("Wait for async job completion on run (default true)"),
  };
  const toolsSchema = tool.schema.object(toolsArgs);

  return tool({
    description:
      "Discover, inspect, and execute 220+ external Agent APIs (SEO, Ahrefs, DataForSEO, Apify scrapers, e-commerce reviews, social media, B2B lead enrichment, AI media) via the Hoxi Tools Gateway. Prefer hoxi_browser / Chrome Link for public web pages or logged-in seller portals (0 points). For structured APIs, follow: discover -> inspect -> run (run requires approval and uses Hoxi Account Points).",
    args: toolsArgs,
    async execute(input, context) {
      const req = toolsSchema.parse(input);
      const rawBase = process.env.HOXI_GATEWAY_URL || process.env.HOXI_BROWSER_URL;
      if (!rawBase) throw new Error("Hoxi gateway URL is unavailable");
      context.abort.throwIfAborted();
      if (req.action === "inspect" && !req.provider) {
        throw new Error("provider is required for hoxi_tools inspect");
      }
      if (req.action === "run") {
        if (!req.provider || !req.endpoint) {
          throw new Error("provider and endpoint are required for hoxi_tools run");
        }
        if (typeof context.ask === "function") {
          await context.ask({
            permission: "hoxi_tools",
            patterns: [`run ${req.provider} ${req.endpoint}`],
            always: [],
            metadata: { tool: "hoxi_tools", ...req, sessionID: context.sessionID, directory: context.directory },
          });
        }
      }
      context.abort.throwIfAborted();
      const base = rawBase.replace(/\/$/, "");
      const url = new URL(`${base}/api/hoxi/tools/${req.action}`);
      const payload = {};
      if (req.action === "discover") {
        if (req.query) payload.query = req.query;
        if (req.category) payload.category = req.category;
        if (req.limit !== undefined) payload.limit = Math.trunc(req.limit);
      } else if (req.action === "inspect") {
        payload.provider = req.provider;
        if (req.endpoint) payload.endpoint = req.endpoint;
      } else {
        payload.provider = req.provider;
        payload.endpoint = req.endpoint;
        if (req.body) payload.body = req.body;
        if (req.queryParams) payload.queryParams = req.queryParams;
        if (req.pathParams) payload.pathParams = req.pathParams;
        if (req.wait !== undefined) payload.wait = req.wait;
      }
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) throw new Error(`Hoxi tools HTTP ${response.status}: ${await response.text()}`);
      const data = await response.json();
      return {
        title: `HoxiTools: ${req.action}${req.provider ? ` (${req.provider})` : ""}`,
        output: JSON.stringify(data),
      };
    },
  });
}
