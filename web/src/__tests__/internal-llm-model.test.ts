import { unwrap } from "solid-js/store";
import {
  isInternalLLMModel,
  resolveInternalLLMModel,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const CLI_MODEL = { providerID: "codebuddy", modelID: "deepseek-v4.1-flash" };
const HTTP_MODEL = { providerID: "ainode", modelID: "deepseek-flash" };

function withProviders(run: () => void): void {
  const saved = structuredClone(unwrap(workspace.providers));
  setWorkspace("providers", [
    { id: "codebuddy", name: "CodeBuddy", source: "runner", models: { "deepseek-v4.1-flash": { name: "Flash" } } },
    { id: "ainode", name: "AINode", source: "api", models: { "deepseek-flash": { name: "DeepSeek Flash" } } },
  ] as never);
  try {
    run();
  } finally {
    setWorkspace("providers", saved);
  }
}

describe("Internal LLM role models", () => {
  it("rejects CLI runtime models that only the runner can drive", () => {
    withProviders(() => {
      assertEqual(isInternalLLMModel(CLI_MODEL), false);
      assertEqual(isInternalLLMModel(HTTP_MODEL), true);
      assertEqual(isInternalLLMModel({ providerID: "ainode", modelID: "missing" }), false);
      assertEqual(isInternalLLMModel({ providerID: "hoxi-auto", modelID: "free" }), false);
      assertEqual(isInternalLLMModel(null), false);
    });
  });

  it("falls back to the first HTTP-capable candidate", () => {
    withProviders(() => {
      assertDeepEqual(resolveInternalLLMModel(CLI_MODEL, HTTP_MODEL), HTTP_MODEL);
      assertDeepEqual(resolveInternalLLMModel(HTTP_MODEL), HTTP_MODEL);
      assertEqual(resolveInternalLLMModel(CLI_MODEL, null, undefined), null);
    });
  });
});
