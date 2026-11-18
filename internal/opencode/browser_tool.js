import { tool } from "./plugin_tool.js";

const actions = [
  "navigate", "back", "forward", "reload", "click", "hover", "type", "select", "key",
  "scroll", "wait", "snapshot", "screenshot", "diagnostics", "evaluate",
  "tab_new", "tab_select", "tab_close", "close",
];

const readOnlyActions = new Set(["snapshot", "screenshot", "diagnostics", "wait"]);

export function createBrowserTool() {
  const args = {
    action: tool.schema.enum(actions),
    url: tool.schema.string().optional().describe("HTTP(S) URL for navigate or tab_new"),
    selector: tool.schema.string().optional().describe("CSS selector or @eN snapshot ref (e.g. @e3) for click, hover, type, select, or wait"),
    text: tool.schema.string().optional().describe("Text to type, option value/label for select, or JavaScript expression for evaluate"),
    key: tool.schema.string().optional().describe("DOM key name (e.g. Enter, Tab), or trailing key after type"),
    clear: tool.schema.boolean().optional().describe("Clear existing input/textarea value before typing (action='type')"),
    timeoutMs: tool.schema.number().int().min(100).max(10000).optional().describe("Bounded wait timeout in ms (100..10000, action='wait')"),
    x: tool.schema.number().finite().optional(),
    y: tool.schema.number().finite().optional(),
    deltaX: tool.schema.number().finite().optional(),
    deltaY: tool.schema.number().finite().optional(),
    tabID: tool.schema.string().optional(),
  };
  const schema = tool.schema.object(args);

  return tool({
    description:
      "Control the active session browser (Desktop Window, isolated headless Chrome, or an attached Chrome Link tab). Call navigate with an HTTP(S) URL to open a page; snapshot, screenshot, and diagnostics only inspect an already-open browser and return empty tabs when idle. snapshot returns visible text plus interactive controls with @e1..@e200 refs and CSS selectors; pass @eN or a CSS selector to click, hover, type, select, or wait. For type, pass clear=true to replace existing text and/or key (e.g. Enter) to submit atomically. Read-only actions (snapshot, screenshot, diagnostics, wait) skip approval; mutations (including evaluate) require approval; manual mode blocks agent control.",
    args,
    async execute(input, context) {
      // Strip unknown fields, including any model-supplied scope or manual override.
      const action = schema.parse(input);
      if (!context.sessionID || !context.directory) throw new Error("Browser session scope is required");
      const rawBase = process.env.HOXI_BROWSER_URL || process.env.HOXI_GATEWAY_URL;
      if (!rawBase || !process.env.HOXI_BROWSER_TOKEN) {
        throw new Error("Hoxi browser runtime is unavailable");
      }
      context.abort.throwIfAborted();
      if (!readOnlyActions.has(action.action)) {
        await context.ask({
          permission: "hoxi_browser",
          patterns: [action.action + " " + JSON.stringify(action)],
          always: [],
          metadata: { ...action, sessionID: context.sessionID, directory: context.directory },
        });
      }
      context.abort.throwIfAborted();
      const url = new URL(rawBase.replace(/\/$/, "") + "/api/hoxi/browser/action");
      url.searchParams.set("sessionID", context.sessionID);
      url.searchParams.set("directory", context.directory);
      const response = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Hoxi-Agent": "true",
          Authorization: "Bearer " + process.env.HOXI_BROWSER_TOKEN,
        },
        body: JSON.stringify(action),
        signal: context.abort,
        redirect: "error",
      });
      if (!response.ok) throw new Error(`Hoxi browser HTTP ${response.status}: ${await response.text()}`);
      const { image, ...state } = await response.json();
      return {
        title: "Browser: " + action.action,
        output: JSON.stringify(state),
        ...(typeof image === "string" && image
          ? {
              attachments: [
                {
                  type: "file",
                  mime: "image/jpeg",
                  filename: "browser.jpg",
                  url: "data:image/jpeg;base64," + image,
                },
              ],
            }
          : {}),
      };
    },
  });
}
