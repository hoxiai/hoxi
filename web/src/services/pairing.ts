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
}

export const pairingAPI = {
  getCode(): Promise<PairingCode> {
    return requestJSON("/api/hoxi/pairing/qr");
  },
  rotate(): Promise<PairingCode> {
    return requestJSON("/api/hoxi/pairing/rotate", { method: "POST", body: "{}" });
  },
};
