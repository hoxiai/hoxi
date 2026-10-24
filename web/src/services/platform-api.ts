// web/src/services/platform-api.ts
import { requestJSON } from "./http";

export interface AuthUserProfile {
  id: number;
  email: string;
  nickname: string;
  username?: string;
  avatar?: string;
  avatarUrl?: string;
  role?: string;
  plan?: string;
}

export interface AuthStatusSnapshot {
  isLoggedIn: boolean;
  isAuthorizing: boolean;
  pendingState?: string;
  user?: AuthUserProfile;
  expiresAt?: string;
  serverBaseUrl: string;
  lastVerifiedAt?: string;
}

export interface StartAuthResponse {
  state: string;
  authUrl: string;
}

export interface UpdateMirrors {
  global?: string;
}

export interface NativeUpdateStatus {
  available: boolean;
  draining: boolean;
}

export interface UpdateCheckResult {
  currentVersion: string;
  latestVersion: string;
  versionCode: number;
  needUpdate: boolean;
  title: string;
  releaseNotes: string;
  downloadUrl: string;
  mirrors: UpdateMirrors;
  mandatory: boolean;
  sha256?: string;
  checkedAt: string;
}

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

export interface ModelPricingItem {
  slug: string;
  name: string;
  vendor: string;
  apiName?: string;
  gatewayModelName?: string;
  aaSlug?: string;
  price: {
    currency: string;
    input: number;
    output: number;
    cacheRead?: number;
    cacheWrite?: number;
  };
  contextWindow: number;
  maxOutputTokens?: number;
  modalities?: string[];
  reasoning?: boolean;
  isFree?: boolean;
  updatedAt?: string;
}

export interface ModelPricingResponse {
  code: number;
  message: string;
  data: {
    updatedAt: string;
    total: number;
    items: ModelPricingItem[];
  };
}

export interface FreeTierGuideRemoteItem {
  id: string;
  providerID: string;
  name: string;
  badge: string;
  quotaLabel: string;
  steps: string[];
  signupUrl: string;
  baseUrl: string;
  customMode?: boolean;
  recommendedModels: string[];
}

export interface FreeTiersResponse {
  code: number;
  message: string;
  data: {
    updatedAt: string;
    total: number;
    items: FreeTierGuideRemoteItem[];
  };
}

export interface MemberCheckinStatus {
  accountLinked?: boolean;
  userId?: number;
  email?: string;
  emailVerified?: boolean;
  gatewayAvailable?: boolean;
  date?: string;
  checkedInToday: boolean;
  alreadyCheckedIn?: boolean;
  streak: number;
  maxStreak?: number;
  todayPoints: number;
  nextPoints: number;
  balance: number;
  walletPoints?: number;
  pendingPoints?: number;
  totalDays?: number;
  rewardStatus?: string;
  rewardOperationId?: string | null;
  rewardError?: string | null;
  rule?: {
    version?: string;
    basePoints: number;
    streakStep: number;
    maxPoints: number;
  };
  milestones?: Array<{ day: number; points: number }>;
  nextMilestone?: { day: number; points: number; daysLeft: number } | null;
}

export type PortalTarget =
  | "dashboard"
  | "rewards"
  | "models"
  | "keys"
  | "billing"
  | "account"
  | "feedback";

export const authAPI = {
  getStatus(): Promise<AuthStatusSnapshot> {
    return requestJSON("/api/hoxi/auth/status");
  },
  startAuth(): Promise<StartAuthResponse> {
    return requestJSON("/api/hoxi/auth/start", { method: "POST" });
  },
  exchange(
    handoffToken: string,
    state: string
  ): Promise<{ success: boolean; user: AuthUserProfile }> {
    return requestJSON("/api/hoxi/auth/exchange", {
      method: "POST",
      body: JSON.stringify({ handoffToken, state }),
    });
  },
  logout(): Promise<{ success: boolean }> {
    return requestJSON("/api/hoxi/auth/logout", { method: "POST" });
  },
  getMemberStatus(): Promise<MemberCheckinStatus> {
    return requestJSON("/api/hoxi/auth/member");
  },
  checkin(): Promise<MemberCheckinStatus> {
    return requestJSON("/api/hoxi/auth/checkin", {
      method: "POST",
      body: JSON.stringify({}),
    });
  },
  openPortalPage(
    target: PortalTarget
  ): Promise<{ success: boolean; url: string }> {
    return requestJSON("/api/hoxi/auth/open-url", {
      method: "POST",
      body: JSON.stringify({ target }),
    });
  },
};

export const updaterAPI = {
  nativeStatus(): Promise<NativeUpdateStatus> {
    return requestJSON("/api/hoxi/updater/native");
  },
  install(): Promise<{ started: true }> {
    return requestJSON("/api/hoxi/updater/install", { method: "POST" });
  },
  checkUpdate(): Promise<UpdateCheckResult> {
    return requestJSON("/api/hoxi/updater/check");
  },
};

export const leaderboardAPI = {
  async getLeaderboard(
    sort: string = "overall",
    limit: number = 10,
    lang: string = "en"
  ): Promise<LeaderboardModelItem[]> {
    const json = await requestJSON<LeaderboardResponse>(
      `/api/hoxi/models/leaderboard?sort=${encodeURIComponent(sort)}&limit=${limit}&lang=${encodeURIComponent(lang)}`
    );
    return json.data?.items || [];
  },
  async getConnectableGateways(
    lang: string = "en"
  ): Promise<ConnectableGatewayItem[]> {
    const json = await requestJSON<ConnectableGatewaysResponse>(
      `/api/hoxi/gateways/connectable?lang=${encodeURIComponent(lang)}`
    );
    return json.data?.items || [];
  },
  async getModelPricing(
    forceRefresh: boolean = false
  ): Promise<ModelPricingItem[]> {
    const q = forceRefresh ? "?refresh=1" : "";
    const json = await requestJSON<ModelPricingResponse>(
      `/api/hoxi/models/pricing${q}`
    );
    return json.data?.items || [];
  },
  async getFreeTiers(
    lang: string = "en"
  ): Promise<FreeTierGuideRemoteItem[]> {
    const json = await requestJSON<FreeTiersResponse>(
      `/api/hoxi/models/free-tiers?lang=${encodeURIComponent(lang)}`
    );
    return json.data?.items || [];
  },
};
