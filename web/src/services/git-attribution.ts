import { requestJSON } from "./http";

/** Which repositories get the `Co-authored-by: Hoxi` trailer. */
export type GitAttributionMode = "auto" | "always" | "never";

export interface GitAttributionSettings {
  mode: GitAttributionMode;
  /** Legacy mirror: false only when mode is `never`. */
  enabled: boolean;
}

/** Commit attribution preference (desktop-only, never remotely toggled). */
export const gitAttributionAPI = {
  get(): Promise<GitAttributionSettings> {
    return requestJSON("/api/hoxi/git/attribution");
  },
  put(mode: GitAttributionMode): Promise<GitAttributionSettings> {
    return requestJSON("/api/hoxi/git/attribution", {
      method: "PUT",
      body: JSON.stringify({ mode }),
    });
  },
};
