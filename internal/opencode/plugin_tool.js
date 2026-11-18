// Minimal @opencode-ai/plugin tool() helper backed by Zod v4 schemas so esbuild
// bundles internal/opencode/browser_plugin.mjs directly from web/node_modules.
import { z } from "zod";

export function tool(input) {
  return input;
}
tool.schema = z;
