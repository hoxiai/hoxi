import { pairingAPI, type PairingCode } from "../services/pairing";
import { loadPairingCode, pairing, rotatePairingToken, setPairing } from "../store/pairing";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setPairing({ code: null, loaded: false, busy: false, error: "" });
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
});
