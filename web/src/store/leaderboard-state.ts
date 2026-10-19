// web/src/store/leaderboard-state.ts
import { createStore } from "solid-js/store";
import { locale } from "../i18n";
import {
  leaderboardAPI,
  type ConnectableGatewayItem,
  type LeaderboardModelItem,
  type ModelPricingItem,
} from "../services/api";

export type LeaderboardTab = "overall" | "coding" | "speed" | "price" | "gateways";

export interface LeaderboardState {
  modalOpen: boolean;
  activeTab: LeaderboardTab;
  models: LeaderboardModelItem[];
  pricingCatalog: ModelPricingItem[];
  gateways: ConnectableGatewayItem[];
  loading: boolean;
  error: string | null;
  updatedAt: string;
}

export const [leaderboardState, setLeaderboardState] = createStore<LeaderboardState>({
  modalOpen: false,
  activeTab: "overall",
  models: [],
  pricingCatalog: [],
  gateways: [],
  loading: false,
  error: null,
  updatedAt: "",
});

function currentLangParam(): string {
  return String(locale() || "en-US").toLowerCase().startsWith("zh") ? "zh" : "en";
}

export async function syncOfficialModelPricing(forceRefresh: boolean = false): Promise<void> {
  if (!forceRefresh && leaderboardState.pricingCatalog.length > 0) return;
  try {
    const items = await leaderboardAPI.getModelPricing(forceRefresh);
    if (Array.isArray(items) && items.length > 0) {
      setLeaderboardState("pricingCatalog", items);
    }
  } catch {
    // Offline fallback handled gracefully by local catalog/prefix inference
  }
}

export async function fetchLeaderboardModels(tab?: LeaderboardTab) {
  const currentTab = tab || leaderboardState.activeTab;
  if (currentTab === "gateways") {
    return fetchGateways();
  }
  setLeaderboardState({ loading: true, error: null });
  try {
    const items = await leaderboardAPI.getLeaderboard(currentTab, 15, currentLangParam());
    setLeaderboardState({ models: items, loading: false });
  } catch (err: any) {
    setLeaderboardState({ error: err?.message || "Failed to load leaderboard", loading: false });
  }
}

export async function fetchGateways() {
  setLeaderboardState({ loading: true, error: null });
  try {
    const items = await leaderboardAPI.getConnectableGateways(currentLangParam());
    setLeaderboardState({ gateways: items, loading: false });
  } catch (err: any) {
    setLeaderboardState({ error: err?.message || "Failed to load gateways", loading: false });
  }
}

export function openLeaderboardModal(tab: LeaderboardTab = "overall") {
  setLeaderboardState({ modalOpen: true, activeTab: tab });
  if (tab === "gateways") {
    void fetchGateways();
  } else {
    void fetchLeaderboardModels(tab);
  }
}

export function closeLeaderboardModal() {
  setLeaderboardState("modalOpen", false);
}

export function switchLeaderboardTab(tab: LeaderboardTab) {
  setLeaderboardState("activeTab", tab);
  if (tab === "gateways") {
    void fetchGateways();
  } else {
    void fetchLeaderboardModels(tab);
  }
}
