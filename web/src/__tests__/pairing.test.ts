import { pairingAPI, type PairingCode, type PublishedShortCode } from "../services/pairing";
import {
  cancelPairingShortCode,
  loadPairingCode,
  pairing,
  publishPairingShortCode,
  rotatePairingToken,
  setPairing,
  setPairingShortCodeRelayUrl,
} from "../store/pairing";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setPairing({
    code: null,
    loaded: false,
    busy: false,
    error: "",
    shortCode: null,
    shortCodeRelayUrl: "",
    shortCodeBusy: false,
    shortCodeError: "",
  });
}

const availableCode: PairingCode = {
  url: "http://192.168.1.20:5417",
  token: "a".repeat(64),
  type: "hoxi",
  entrance: "",
  host: "192.168.1.20",
  port: 5417,
  hostname: "Studio",
  available: true,
  qrPng: "data:image/png;base64,AAAA",
};

const sampleShortCode: PublishedShortCode = {
  code: "482910",
  expiresAtMs: Date.now() + 60_000,
  relayUrl: "https://link.hoxi.example",
};

describe("lan pairing code", () => {
  it("loads the pairing code from the gateway", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.getCode = async () => availableCode;
      await loadPairingCode();
      assertEqual(pairing.loaded, true);
      assertEqual(pairing.error, "");
      assertEqual(pairing.code?.url, availableCode.url);
      assertEqual(pairing.code?.token, availableCode.token);
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("surfaces the unavailable state without a QR image", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.getCode = async () => ({ ...availableCode, url: "", qrPng: "", available: false });
      await loadPairingCode();
      assertEqual(pairing.code?.available, false);
      assertEqual(pairing.code?.qrPng, "");
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("reports a load error and stops the busy state", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.getCode = async () => {
        throw new Error("HTTP 500: boom");
      };
      await loadPairingCode();
      assertEqual(pairing.busy, false);
      assertTrue(pairing.error.length > 0, "error message should be set");
      assertEqual(pairing.code, null);
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("rotates the pairing token", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.getCode = async () => availableCode;
      await loadPairingCode();
      pairingAPI.rotate = async () => ({ ...availableCode, token: "b".repeat(64) });
      await rotatePairingToken();
      assertEqual(pairing.busy, false);
      assertEqual(pairing.code?.token, "b".repeat(64));
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("does not let an in-flight loadPairingCode block rotatePairingToken or overwrite it", async () => {
    const api = { ...pairingAPI };
    let resolveLoad!: (code: PairingCode) => void;
    try {
      pairingAPI.getCode = () => new Promise<PairingCode>((r) => { resolveLoad = r; });
      pairingAPI.rotate = async () => ({ ...availableCode, token: "c".repeat(64) });

      const loadPromise = loadPairingCode();
      const rotatePromise = rotatePairingToken();
      await rotatePromise;
      assertEqual(pairing.code?.token, "c".repeat(64));

      resolveLoad(availableCode);
      await loadPromise;
      assertEqual(pairing.code?.token, "c".repeat(64), "stale load must not overwrite rotated token");
      assertEqual(pairing.busy, false);
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("publishes, cancels, and clears a 6-digit short code", async () => {
    const api = { ...pairingAPI };
    let cancelled = false;
    try {
      await publishPairingShortCode("");
      assertTrue(pairing.shortCodeError.length > 0, "empty relayUrl should report validation error");

      pairingAPI.publishShortCode = async (relayUrl) => ({ ...sampleShortCode, relayUrl });
      pairingAPI.cancelShortCode = async () => {
        cancelled = true;
      };
      setPairingShortCodeRelayUrl("https://link.hoxi.example");
      await publishPairingShortCode();
      assertEqual(pairing.shortCodeBusy, false);
      assertEqual(pairing.shortCodeError, "");
      assertEqual(pairing.shortCode?.code, "482910");

      await cancelPairingShortCode();
      assertEqual(cancelled, true);
      assertEqual(pairing.shortCode, null);
      assertEqual(pairing.shortCodeBusy, false);
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("clears an active short code when the pairing token rotates", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.publishShortCode = async () => sampleShortCode;
      pairingAPI.rotate = async () => ({ ...availableCode, token: "d".repeat(64) });
      await publishPairingShortCode("https://link.hoxi.example");
      assertEqual(pairing.shortCode?.code, "482910");

      await rotatePairingToken();
      assertEqual(pairing.shortCode, null);
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });

  it("prefills shortCodeRelayUrl from backend default and migrates retired link.hoxi.ai", async () => {
    const api = { ...pairingAPI };
    try {
      pairingAPI.getCode = async () => ({
        ...availableCode,
        shortCodeRelayUrl: "https://relay.hoxi.example",
      });
      await loadPairingCode();
      assertEqual(pairing.shortCodeRelayUrl, "https://relay.hoxi.example");

      setPairingShortCodeRelayUrl("https://link.hoxi.ai/");
      await loadPairingCode();
      assertEqual(pairing.shortCodeRelayUrl, "https://relay.hoxi.example");

      setPairingShortCodeRelayUrl("https://selfhosted.example");
      await loadPairingCode();
      assertEqual(pairing.shortCodeRelayUrl, "https://selfhosted.example");
    } finally {
      Object.assign(pairingAPI, api);
      reset();
    }
  });
});
