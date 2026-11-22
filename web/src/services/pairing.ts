// web/src/services/pairing.ts
// LAN pairing code the desktop shows as a QR for the Hoxi mobile app. The
// token is the desktop's own secret and stays on this machine.
import { requestJSON } from "./http";

export interface PairingCode {
  url: string;
  token: string;
  type: string;
  entrance: string;
  host: string;
  port: number;
  hostname: string;
  available: boolean;
  /** data: URL PNG of the QR code; empty when no LAN address was found. */
  qrPng: string;
  /** Build/env default 6-digit pairing relay URL (empty in OSS builds). */
  shortCodeRelayUrl?: string;
}

export interface PublishedShortCode {
  code: string;
  expiresAtMs: number;
  relayUrl: string;
}

export const pairingAPI = {
  getCode(): Promise<PairingCode> {
    return requestJSON("/api/hoxi/pairing/qr");
  },
  rotate(): Promise<PairingCode> {
    return requestJSON("/api/hoxi/pairing/rotate", { method: "POST", body: "{}" });
  },
  publishShortCode(relayUrl: string): Promise<PublishedShortCode> {
    return requestJSON("/api/hoxi/pairing/short-code", {
      method: "POST",
      body: JSON.stringify({ relayUrl }),
    });
  },
  cancelShortCode(): Promise<void> {
    return requestJSON<void>("/api/hoxi/pairing/short-code", { method: "DELETE" });
  },
};
