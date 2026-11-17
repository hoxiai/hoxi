// web/src/store/leaderboard-state.ts
import { createStore } from "solid-js/store";
import { locale } from "../i18n";
import {
  leaderboardAPI,
  type ConnectableGatewayItem,
  type ModelPricingItem,
} from "../services/api";

export interface LeaderboardState {
  pricingCatalog: ModelPricingItem[];
  gateways: ConnectableGatewayItem[];
  gatewaysError: string | null;
  loading: boolean;
}

export const [leaderboardState, setLeaderboardState] = createStore<LeaderboardState>({
  pricingCatalog: [],
  gateways: [],
  gatewaysError: null,
  loading: false,
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

export async function fetchGateways() {
  setLeaderboardState({ loading: true, gatewaysError: null });
  try {
    const items = await leaderboardAPI.getConnectableGateways(currentLangParam());
    setLeaderboardState({ gateways: items, gatewaysError: null, loading: false });
  } catch (err: any) {
    setLeaderboardState({
      gateways: [],
      gatewaysError: err?.message || "Failed to load gateways",
      loading: false,
    });
  }
}
