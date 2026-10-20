// web/src/services/skills.ts
import { requestJSON } from "./runner-api";
import type { SkillDetail, SkillListResponse } from "./skills-types";

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
};
