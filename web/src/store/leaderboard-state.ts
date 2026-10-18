// web/src/store/leaderboard-state.ts
import { createStore } from "solid-js/store";
import {
  leaderboardAPI,
  type ConnectableGatewayItem,
  type LeaderboardModelItem,
} from "../services/api";

export type LeaderboardTab = "overall" | "coding" | "speed" | "price" | "gateways";

export interface LeaderboardState {
  modalOpen: boolean;
  activeTab: LeaderboardTab;
  models: LeaderboardModelItem[];
  gateways: ConnectableGatewayItem[];
  loading: boolean;
  error: string | null;
  updatedAt: string;
}

export const [leaderboardState, setLeaderboardState] = createStore<LeaderboardState>({
  modalOpen: false,
  activeTab: "overall",
  models: [],
  gateways: [],
  loading: false,
  error: null,
  updatedAt: "",
});

export async function fetchLeaderboardModels(tab?: LeaderboardTab) {
  const currentTab = tab || leaderboardState.activeTab;
  if (currentTab === "gateways") {
    return fetchGateways();
  }
  setLeaderboardState({ loading: true, error: null });
  try {
    const items = await leaderboardAPI.getLeaderboard(currentTab, 15);
    setLeaderboardState({ models: items, loading: false });
  } catch (err: any) {
    setLeaderboardState({ error: err?.message || "Failed to load leaderboard", loading: false });
  }
}

export async function fetchGateways() {
  setLeaderboardState({ loading: true, error: null });
  try {
    const items = await leaderboardAPI.getConnectableGateways();
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
