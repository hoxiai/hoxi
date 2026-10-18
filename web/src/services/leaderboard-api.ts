// web/src/services/leaderboard-api.ts

export interface LeaderboardModelItem {
  rank: number;
  slug: string;
  name: string;
  vendor: string;
  apiName: string;
  price: {
    currency: string;
    input: number;
    output: number;
    cacheRead?: number;
    cacheWrite?: number;
  };
  contextWindow: number;
  scores: {
    overall?: number;
    coding?: number;
    reasoning?: number;
    chinese?: number;
    agentic?: number;
  };
  perf: {
    tps?: number;
    ttft?: number;
  };
  badges: string[];
  scenes: string[];
  summary: string;
  reasoning?: boolean;
  isFree?: boolean;
}

export interface ConnectableGatewayItem {
  id: string;
  name: string;
  domain: string;
  url: string;
  baseUrl: string;
  isSelfOperated: boolean;
  canOneClickConnect: boolean;
  badge?: string;
  badgeTone?: "positive" | "info" | "warning";
  savingsPercent: number;
  priceLabel: string;
  officialPriceCNY: number;
  latencyMs: number;
  uptime: string;
  models: string[];
  recommendedModels: string[];
  features: string[];
  caveat: string;
  authUrl?: string;
}

export interface LeaderboardResponse {
  code: number;
  message: string;
  data: {
    dimension: string;
    updatedAt: string;
    total: number;
    items: LeaderboardModelItem[];
  };
}

export interface ConnectableGatewaysResponse {
  code: number;
  message: string;
  data: {
    total: number;
    items: ConnectableGatewayItem[];
  };
}

export const leaderboardAPI = {
  async getLeaderboard(sort: string = "overall", limit: number = 10): Promise<LeaderboardModelItem[]> {
    const res = await fetch(`/api/hoxi/models/leaderboard?sort=${encodeURIComponent(sort)}&limit=${limit}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const json: LeaderboardResponse = await res.json();
    return json.data?.items || [];
  },

  async getConnectableGateways(): Promise<ConnectableGatewayItem[]> {
    const res = await fetch(`/api/hoxi/gateways/connectable`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const json: ConnectableGatewaysResponse = await res.json();
    return json.data?.items || [];
  },
};
