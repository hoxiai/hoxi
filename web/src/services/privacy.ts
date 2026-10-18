import { requestJSON } from "./runner-api";

export const PRIVACY_VERSION = "2026-10-18";

export interface PrivacyConsent {
  version: string;
  accepted: boolean;
}

export const privacyAPI = {
  get(): Promise<PrivacyConsent> {
    return requestJSON("/api/hoxi/privacy");
  },
  put(accepted: boolean): Promise<PrivacyConsent> {
    return requestJSON("/api/hoxi/privacy", {
      method: "PUT",
      body: JSON.stringify({ version: PRIVACY_VERSION, accepted }),
    });
  },
};
