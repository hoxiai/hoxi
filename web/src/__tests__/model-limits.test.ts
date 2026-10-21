import { unwrap } from "solid-js/store";
import { setLeaderboardState } from "../store/leaderboard-state";
import {
  contextLimitForSelection,
  lookupModelContextLimit,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

function withCatalog(run: () => void): void {
  const providers = structuredClone(unwrap(workspace.providers));
  const config = structuredClone(unwrap(workspace.globalConfig));
  setWorkspace("providers", [
    {
      id: "opencode",
      name: "OpenCode",
      models: {
        "deepseek-v4-flash": { name: "DeepSeek V4 Flash" },
        "claude-sonnet": { name: "Claude Sonnet", limit: { context: 200_000 } },
      },
    },
    {
      id: "codebuddy",
      name: "CodeBuddy",
      source: "runner",
      models: {
        "deepseek-v4-flash": { name: "DeepSeek V4 Flash", limit: { context: 1_000_000 } },
      },
    },
  ] as never);
  setWorkspace("globalConfig", {});
  try {
    run();
  } finally {
    setWorkspace("providers", providers);
    setWorkspace("globalConfig", config);
  }
}

describe("Model context limits", () => {
  it("uses a sibling catalog window when the selected model omits one", () => {
    withCatalog(() => {
      assertEqual(lookupModelContextLimit("opencode", "deepseek-v4-flash"), 1_000_000);
      assertEqual(contextLimitForSelection("opencode", "deepseek-v4-flash"), 1_000_000);
    });
  });

  it("keeps a declared window instead of borrowing another provider", () => {
    withCatalog(() => {
      assertEqual(contextLimitForSelection("opencode", "claude-sonnet"), 200_000);
    });
  });

  it("infers the family window when no provider declares one", () => {
    withCatalog(() => {
      assertEqual(contextLimitForSelection("opencode", "gemini-2.5-flash"), 1_000_000);
    });
  });

  it("falls back to 200k only when nothing identifies the model", () => {
    withCatalog(() => {
      assertEqual(lookupModelContextLimit("opencode", "custom-local"), undefined);
      assertEqual(contextLimitForSelection("opencode", "custom-local"), 200_000);
    });
  });

  it("prefers the official pricing window over family inference", () => {
    withCatalog(() => {
      setLeaderboardState("pricingCatalog", [
        {
          slug: "kimi-k2",
          name: "Kimi K2",
          vendor: "moonshot",
          contextWindow: 256_000,
          price: { currency: "USD", input: 0.6, output: 2.5 },
        },
      ] as never);
      try {
        assertEqual(contextLimitForSelection("opencode", "kimi-k2"), 256_000);
      } finally {
        setLeaderboardState("pricingCatalog", []);
      }
    });
  });
});
