import { requestJSON } from "./http";

export interface GitAttributionSettings {
  enabled: boolean;
}

/** Whether Hoxi-created commits carry the `Co-authored-by: Hoxi` trailer (desktop-only). */
export const gitAttributionAPI = {
  get(): Promise<GitAttributionSettings> {
    return requestJSON("/api/hoxi/git/attribution");
  },
  put(enabled: boolean): Promise<GitAttributionSettings> {
    return requestJSON("/api/hoxi/git/attribution", {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    });
  },
};
