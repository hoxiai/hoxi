import { tool } from "@opencode-ai/plugin";

const actions = [
  "navigate", "back", "forward", "reload", "click", "type", "key",
  "scroll", "snapshot", "screenshot", "evaluate", "tab_new", "tab_select", "tab_close", "close",
];

export function createBrowserTool() {
  const args = {
    action: tool.schema.enum(actions),
    url: tool.schema.string().optional().describe("HTTP(S) URL for navigate or tab_new"),
    selector: tool.schema.string().optional().describe("CSS selector for click or type"),
    text: tool.schema.string().optional().describe("Text to type, or JavaScript expression for evaluate"),
    key: tool.schema.string().optional().describe("DOM key name, e.g. Enter"),
    x: tool.schema.number().finite().optional(),
    y: tool.schema.number().finite().optional(),
    deltaX: tool.schema.number().finite().optional(),
    deltaY: tool.schema.number().finite().optional(),
    tabID: tool.schema.string().optional(),
  };
  const schema = tool.schema.object(args);

  return tool({
    description:
      "Control the session's embedded browser. Use snapshot for visible text and CSS selectors, screenshot for a JPEG, and evaluate for JavaScript (requires approval). Mutations require approval; manual mode blocks agent control.",
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
      if (action.action !== "snapshot" && action.action !== "screenshot") {
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
