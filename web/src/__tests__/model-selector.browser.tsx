import { render } from "solid-js/web";
import ModelSelector from "../components/models/ModelSelector";
import { t } from "../i18n";
import { hoxiAPI, runnerAPI } from "../services/api";
import { resolveEffectiveModelEffort } from "../store/provider-catalog";
import { loadProviders } from "../store/workspace-providers";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

export async function runBrowserTest(): Promise<string[]> {
  // Reproduce the live Codex catalog plus a legacy saved Hoxi config. All
  // requests stay in this fixture; changing effort must never dispatch a turn.
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  const model = { providerID: "openai", modelID: "gpt-6.1-sol" };
  const variants = Object.fromEntries(["low", "medium", "high", "xhigh", "max"].map(
    (effort) => [effort, { reasoningEffort: effort }]
  ));
  hoxiAPI.listProviders = async () => ({
    all: [{ id: "openai", name: "Codex", models: {
      [model.modelID]: { id: model.modelID, name: "GPT-6.1 Sol (Codex)", reasoning: true, variants },
    } }], connected: ["openai"], default: {},
  });
  hoxiAPI.getGlobalConfig = async () => ({});
  hoxiAPI.getProviderAuthMethods = async () => ({});
  hoxiAPI.getHoxiModelsConfig = async () => ({
    hoxiProviders: { openai: { models: {
      [model.modelID]: { reasoning: true, variants: {
        low: { reasoningEffort: "low" }, medium: { reasoningEffort: "medium" }, high: { reasoningEffort: "high" },
      } },
    } } }, openCodeProviders: [], configPath: "test",
  });
  runnerAPI.listModels = async () => ({ installed: false, available: false, models: [], default: "" });
  setWorkspace({ activeSessionID: null, defaultAI: { model, agent: "build", effort: "high" } });
  await loadProviders(true);
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;left:20px;bottom:20px";
  document.body.append(host);
  const dispose = render(() => <ModelSelector />, host);
  const results: string[] = [];
  try {
    host.querySelector("button")!.click();
    await frame();
    await frame();
    const labels = (["effortAuto", "effortLow", "effortMedium", "effortHigh", "effortXHigh", "effortMax"] as const)
      .map((key) => t(`models.${key}`));
    const buttons = () => Array.from(document.querySelectorAll<HTMLButtonElement>("button[aria-pressed]"))
      .filter((button) => labels.includes(button.textContent?.trim() || ""));
    assertEqual(buttons().length, 6, "picker must render Auto plus all five reasoning levels");
    for (const option of [
      { level: "xhigh", label: t("models.effortXHigh") },
      { level: "max", label: t("models.effortMax") },
    ] as const) {
      const button = buttons().find((item) => item.textContent?.trim() === option.label);
      assertTrue(Boolean(button), `${option.level} button is missing`);
      const rect = button!.getBoundingClientRect();
      assertTrue(rect.width > 0 && rect.left >= 0 && rect.right <= innerWidth, `${option.level} must be visible`);
      button!.click();
      await frame();
      assertEqual(workspace.reasoningEffort, option.level, "selection must retain the requested level");
      assertEqual(button!.getAttribute("aria-pressed"), "true", "selected button must stay active");
      assertEqual(resolveEffectiveModelEffort(model, workspace.reasoningEffort), option.level, "dispatch must preserve selected effort");
      results.push(`loaded legacy Codex config: ${option.level} is visible, selectable and retained for dispatch`);
    }
  } finally {
    dispose();
    host.remove();
  }
  return results;
}
