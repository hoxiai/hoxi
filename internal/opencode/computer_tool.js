import { tool } from "@opencode-ai/plugin";

export function createComputerTool() {
  const args = {
    action: tool.schema.enum(["status", "apps", "state", "screenshot", "focus", "move", "click", "drag", "scroll", "type", "key"]),
    app: tool.schema.string().max(256).optional(),
    windowID: tool.schema.string().max(64).optional(),
    observationID: tool.schema.string().max(64).optional(),
    elementID: tool.schema.number().int().min(1).max(300).optional(),
    x: tool.schema.number().finite().optional(), y: tool.schema.number().finite().optional(),
    endX: tool.schema.number().finite().optional(), endY: tool.schema.number().finite().optional(),
    deltaX: tool.schema.number().int().optional(), deltaY: tool.schema.number().int().optional(),
    button: tool.schema.enum(["left", "right", "middle"]).optional(),
    clickCount: tool.schema.number().int().min(1).max(3).optional(),
    text: tool.schema.string().max(8192).optional(), key: tool.schema.string().max(128).optional(),
  };
  const schema = tool.schema.object(args);
  return tool({
    description: "Inspect and control a running desktop app on macOS and Windows. Use the app the user authorized in Hoxi's Computer panel, or pass 'app' (app name like 'Calculator' or 'Safari', bundleID, or id from action='apps') on screenshot/state to ask for a different app: the user must confirm that app in a native dialog, and may decline. Use screenshot/state first; each element includes windowBounds (relative to the target window) and an id you can pass as elementID to click, move, scroll, or type without manual coordinate math. Manual x/y coordinates are relative to the window's bounds, not the scaled screenshot. Every input requires the returned windowID and observationID, waits for a one-time approval on the user's desktop, and consumes the observation; inspect again before the next action. Protected apps (Hoxi, System Settings, terminals and script editors, password managers) and password fields are blocked.",
    args,
    async execute(input, context) {
      if (!context.sessionID || !context.directory) throw new Error("Computer use requires a Hoxi session scope");
      const base = process.env.HOXI_GATEWAY_URL;
      const token = process.env.HOXI_BROWSER_TOKEN;
      if (!base || !token) throw new Error("Hoxi desktop computer control is unavailable");
      const action = schema.parse(input);
      context.abort.throwIfAborted();
      const url = new URL(base.replace(/\/$/, "") + "/api/hoxi/computer/action");
      url.searchParams.set("sessionID", context.sessionID);
      url.searchParams.set("directory", context.directory);
      // Input approval is enforced by Go through /api/hoxi/permission, including
      // callers that do not use this plugin. Never replace it with client-only approval.
      const response = await fetch(url, { method: "POST", redirect: "error", signal: context.abort,
        headers: { "Content-Type": "application/json", "X-Hoxi-Agent": "true", Authorization: "Bearer " + token }, body: JSON.stringify(action) });
      if (!response.ok) throw new Error(`Hoxi computer HTTP ${response.status}: ${await response.text()}`);
      const { image, ...state } = await response.json();
      return { title: "Computer: " + action.action, output: JSON.stringify(state),
        ...(typeof image === "string" && image ? { attachments: [{ type: "file", mime: "image/jpeg", filename: "computer.jpg", url: "data:image/jpeg;base64," + image }] } : {}) };
    },
  });
}
