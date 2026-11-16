import { leaderboardAPI, type ConnectableGatewayItem } from "../services/api";
import { fetchGateways, leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

const sample: ConnectableGatewayItem[] = [
  {
    id: "ainode-pro",
    name: "AINode",
    domain: "ainode.run",
    url: "https://api.ainode.run",
    baseUrl: "https://api.ainode.run/v1",
    isSelfOperated: true,
    canOneClickConnect: true,
    savingsPercent: 0,
    priceLabel: "",
    officialPriceCNY: 0,
    latencyMs: 0,
    uptime: "",
    models: [],
    recommendedModels: [],
    features: [],
    caveat: "",
  },
];

function reset() {
  setLeaderboardState({ gateways: [], gatewaysError: null, loading: false });
}

describe("gateway directory", () => {
  it("fetches the recommended providers on every call instead of caching", async () => {
    const original = leaderboardAPI.getConnectableGateways;
    let hits = 0;
    try {
      leaderboardAPI.getConnectableGateways = async () => {
        hits++;
        return sample;
      };
      await fetchGateways();
      assertEqual(hits, 1);
      assertEqual(leaderboardState.gateways.length, 1);
      assertEqual(leaderboardState.gatewaysError, null);

      await fetchGateways();
      assertEqual(hits, 2);
      assertEqual(leaderboardState.gateways.length, 1);
    } finally {
      leaderboardAPI.getConnectableGateways = original;
      reset();
    }
  });

  it("clears the list and records an error when the directory is unreachable", async () => {
    const original = leaderboardAPI.getConnectableGateways;
    try {
      leaderboardAPI.getConnectableGateways = async () => {
        throw new Error("HTTP 500: gateway directory unavailable");
      };
      await fetchGateways();
      assertEqual(leaderboardState.gateways.length, 0);
      assertTrue(Boolean(leaderboardState.gatewaysError), "expected a gateways error to surface");
      assertEqual(leaderboardState.loading, false);
    } finally {
      leaderboardAPI.getConnectableGateways = original;
      reset();
    }
  });
});
