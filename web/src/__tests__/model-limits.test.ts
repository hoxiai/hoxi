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

  it("matches a deployment variant to its family window", () => {
    withCatalog(() => {
      assertEqual(contextLimitForSelection("subapi", "deepseek-v4.1-flash-solo"), 1_048_576);
      assertEqual(contextLimitForSelection("subapi", "glm-5.3-flash-solo"), 1_000_000);
      assertEqual(contextLimitForSelection("subapi", "mimo-v2.6-pro-solo"), 1_048_576);
      assertEqual(contextLimitForSelection("subapi", "step-5-preview-solo"), 1_000_000);
    });
  });

  it("ignores a placeholder pricing row that is far below the family window", () => {
    withCatalog(() => {
      setLeaderboardState("pricingCatalog", [
        {
          slug: "deepseek-v4-1-flash",
          name: "DeepSeek V4.1 Flash",
          vendor: "deepseek",
          contextWindow: 128_000,
          maxOutputTokens: 8_192,
          price: { currency: "USD", input: 0.3, output: 1.2 },
        },
      ] as never);
      try {
        assertEqual(contextLimitForSelection("subapi", "deepseek-v4.1-flash-solo"), 1_048_576);
      } finally {
        setLeaderboardState("pricingCatalog", []);
      }
    });
  });

  it("keeps a reseller window that is larger than the family window", () => {
    withCatalog(() => {
      setLeaderboardState("pricingCatalog", [
        {
          slug: "deepseek-v4-1-flash",
          name: "DeepSeek V4.1 Flash",
          vendor: "deepseek",
          contextWindow: 2_000_000,
          price: { currency: "USD", input: 0.3, output: 1.2 },
        },
      ] as never);
      try {
        assertEqual(contextLimitForSelection("subapi", "deepseek-v4.1-flash-solo"), 2_000_000);
      } finally {
        setLeaderboardState("pricingCatalog", []);
      }
    });
  });

  it("does not let a free variant borrow the paid variant's window", () => {
    withCatalog(() => {
      setWorkspace("providers", [
        {
          id: "opencode",
          name: "OpenCode",
          models: {
            "deepseek-v4.1-flash": { name: "DeepSeek V4.1 Flash", limit: { context: 1_048_576 } },
            "deepseek-v4.1-flash-free": { name: "DeepSeek V4.1 Flash Free", limit: { context: 200_000 } },
          },
        },
      ] as never);
      try {
        assertEqual(contextLimitForSelection("opencode", "deepseek-v4.1-flash"), 1_048_576);
        assertEqual(contextLimitForSelection("opencode", "deepseek-v4.1-flash-free"), 200_000);
      } finally {
        setWorkspace("providers", []);
      }
    });
  });

  it("uses a declared window from the Hoxi models config", () => {
    withCatalog(() => {
      setWorkspace("providers", [
        { id: "subapi", name: "Subapi", models: { "mystery-model-9": { name: "Mystery" } } },
      ] as never);
      setWorkspace("globalConfig", {
        provider: { subapi: { models: { "mystery-model-9": { limit: { context: 640_000 } } } } },
      });
      try {
        assertEqual(contextLimitForSelection("subapi", "mystery-model-9"), 640_000);
      } finally {
        setWorkspace("providers", []);
        setWorkspace("globalConfig", {});
      }
    });
  });
});

describe("Vendor-prefixed and channel model ids", () => {
  it("infers the family window from a vendor-prefixed id", () => {
    withCatalog(() => {
      assertEqual(contextLimitForSelection("subapi", "OfficeAce-deepseek-v4.1-flash"), 1_048_576);
      assertEqual(contextLimitForSelection("subapi", "OfficeAce-gpt-6-astra"), 1_050_000);
      assertEqual(contextLimitForSelection("subapi", "OfficeAce-claude-opus-5-5"), 1_000_000);
      assertEqual(contextLimitForSelection("subapi", "QoderCN-DeepSeek-Flash"), 1_048_576);
    });
  });

  it("does not invent a window for an id that only looks like a family", () => {
    withCatalog(() => {
      assertEqual(lookupModelContextLimit("subapi", "xstep-9"), undefined);
      assertEqual(lookupModelContextLimit("subapi", "internal-glmmodel"), undefined);
      assertEqual(contextLimitForSelection("subapi", "xstep-9"), 200_000);
    });
  });

  it("keeps the o-series and gpt version detection working", () => {
    withCatalog(() => {
      assertEqual(contextLimitForSelection("openai", "o3-mini"), 400_000);
      assertEqual(contextLimitForSelection("openai", "gpt-5-6-terra"), 1_050_000);
      assertEqual(contextLimitForSelection("openai", "gpt-6.1-sol"), 1_050_000);
    });
  });
});
