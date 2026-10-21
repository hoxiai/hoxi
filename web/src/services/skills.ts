// web/src/services/skills.ts
import { requestJSON } from "./http";
import type {
  MarketplaceInstallResponse,
  MarketplaceSkillListResponse,
  SkillDetail,
  SkillListResponse,
} from "./skills-types";

export const skillsAPI = {
  fetchSkillsList(params?: {
    directory?: string;
    sessionID?: string | null;
    runtime?: string;
  }): Promise<SkillListResponse> {
    const query = new URLSearchParams();
    if (params?.directory) query.set("directory", params.directory);
    if (params?.sessionID) query.set("sessionID", params.sessionID);
    if (params?.runtime) query.set("runtime", params.runtime);
    const qs = query.toString();
    return requestJSON<SkillListResponse>(`/api/hoxi/skills/list${qs ? `?${qs}` : ""}`);
  },

  fetchSkillDetail(params: {
    key: string;
    directory?: string;
    sessionID?: string | null;
    runtime?: string;
  }): Promise<SkillDetail> {
    const query = new URLSearchParams();
    query.set("key", params.key);
    if (params.directory) query.set("directory", params.directory);
    if (params.sessionID) query.set("sessionID", params.sessionID);
    if (params.runtime) query.set("runtime", params.runtime);
    return requestJSON<SkillDetail>(`/api/hoxi/skills/detail?${query.toString()}`);
  },

  fetchMarketplaceSkills(params?: { refresh?: boolean }): Promise<MarketplaceSkillListResponse> {
    const query = new URLSearchParams();
    if (params?.refresh) query.set("refresh", "1");
    const qs = query.toString();
    return requestJSON<MarketplaceSkillListResponse>(
      `/api/hoxi/skills/marketplace${qs ? `?${qs}` : ""}`
    );
  },

  installMarketplaceSkill(params: {
    slug: string;
    overwrite?: boolean;
  }): Promise<MarketplaceInstallResponse> {
    return requestJSON<MarketplaceInstallResponse>("/api/hoxi/skills/marketplace/install", {
      method: "POST",
      body: JSON.stringify({ slug: params.slug, overwrite: Boolean(params.overwrite) }),
    });
  },
};
