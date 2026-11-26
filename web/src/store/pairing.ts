// web/src/store/pairing.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { errorMessageOf } from "../services/http";
import { pairingAPI, type PairingCode, type PublishedShortCode } from "../services/pairing";

const SHORT_CODE_RELAY_STORAGE_KEY = "hoxi.pairing.relayUrl";

function readStoredRelayUrl(): string {
  try {
    if (typeof localStorage === "undefined") return "";
    return (localStorage.getItem(SHORT_CODE_RELAY_STORAGE_KEY) || "").trim();
  } catch {
    return "";
  }
}

function writeStoredRelayUrl(url: string): void {
  try {
    if (typeof localStorage === "undefined") return;
    if (url) {
      localStorage.setItem(SHORT_CODE_RELAY_STORAGE_KEY, url);
    } else {
      localStorage.removeItem(SHORT_CODE_RELAY_STORAGE_KEY);
    }
  } catch {
    /* ignore storage failures */
  }
}

export const [pairing, setPairing] = createStore<{
  code: PairingCode | null;
  loaded: boolean;
  busy: boolean;
  error: string;
  shortCode: PublishedShortCode | null;
  shortCodeRelayUrl: string;
  shortCodeBusy: boolean;
  shortCodeError: string;
}>({
  code: null,
  loaded: false,
  busy: false,
  error: "",
  shortCode: null,
  shortCodeRelayUrl: readStoredRelayUrl(),
  shortCodeBusy: false,
  shortCodeError: "",
});

let pairingGen = 0;
let shortCodeGen = 0;
let rotating = false;

export function setPairingShortCodeRelayUrl(url: string): void {
  const next = url.trim();
  setPairing({ shortCodeRelayUrl: next, shortCodeError: "" });
  writeStoredRelayUrl(next);
}

function applyDefaultShortCodeRelay(defaultRelay: string): { shortCodeRelayUrl?: string } {
  const next = defaultRelay.trim().replace(/\/+$/, "");
  if (!next) return {};
  const current = pairing.shortCodeRelayUrl.trim().replace(/\/+$/, "");
  if (!current) {
    return { shortCodeRelayUrl: next };
  }
  if (current.toLowerCase() === "https://link.hoxi.ai" && current.toLowerCase() !== next.toLowerCase()) {
    writeStoredRelayUrl(next);
    return { shortCodeRelayUrl: next };
  }
  return {};
}

// Fetches the current LAN pairing code. The QR is regenerated on every call so
// the displayed address follows the machine's network.
export async function loadPairingCode(): Promise<void> {
  if (rotating) return;
  const gen = ++pairingGen;
  setPairing({ busy: true, error: "" });
  try {
    const code = await pairingAPI.getCode();
    if (gen !== pairingGen) return;
    setPairing({
      code,
      loaded: true,
      error: "",
      ...applyDefaultShortCodeRelay(code.shortCodeRelayUrl || ""),
    });
  } catch {
    if (gen !== pairingGen) return;
    setPairing({ loaded: true, error: t("pairing.loadError") });
  } finally {
    if (gen === pairingGen) {
      setPairing("busy", false);
    }
  }
}

// Replaces the pairing token and refreshes the shown code; any previously
// scanned QR or saved LAN connection stops working.
export async function rotatePairingToken(): Promise<void> {
  if (rotating) return;
  rotating = true;
  const gen = ++pairingGen;
  shortCodeGen++;
  setPairing({ busy: true, error: "", shortCode: null, shortCodeError: "" });
  try {
    const code = await pairingAPI.rotate();
    if (gen === pairingGen) {
      setPairing({
        code,
        loaded: true,
        error: "",
        ...applyDefaultShortCodeRelay(code.shortCodeRelayUrl || ""),
      });
    }
  } catch {
    if (gen === pairingGen) {
      setPairing({ loaded: true, error: t("pairing.rotateError") });
    }
  } finally {
    rotating = false;
    if (gen === pairingGen) {
      setPairing("busy", false);
    }
  }
}

// Publishes a 60-second zero-knowledge 6-digit pairing code to the configured relay.
export async function publishPairingShortCode(relayUrlOverride?: string): Promise<void> {
  const relayUrl = (relayUrlOverride ?? pairing.shortCodeRelayUrl).trim();
  if (!relayUrl) {
    setPairing("shortCodeError", t("pairing.shortCodeRelayRequired"));
    return;
  }
  setPairingShortCodeRelayUrl(relayUrl);
  const gen = ++shortCodeGen;
  setPairing({ shortCodeBusy: true, shortCodeError: "" });
  try {
    const published = await pairingAPI.publishShortCode(relayUrl);
    if (gen !== shortCodeGen) return;
    setPairing({ shortCode: published, shortCodeError: "" });
  } catch (err) {
    if (gen !== shortCodeGen) return;
    setPairing("shortCodeError", errorMessageOf(err, t("pairing.shortCodePublishError")));
  } finally {
    if (gen === shortCodeGen) {
      setPairing("shortCodeBusy", false);
    }
  }
}

// Revokes the active 6-digit pairing code on the relay and clears local state.
export async function cancelPairingShortCode(): Promise<void> {
  const gen = ++shortCodeGen;
  setPairing({ shortCodeBusy: true, shortCodeError: "" });
  try {
    await pairingAPI.cancelShortCode();
    if (gen !== shortCodeGen) return;
    setPairing({ shortCode: null, shortCodeError: "" });
  } catch (err) {
    if (gen !== shortCodeGen) return;
    setPairing("shortCodeError", errorMessageOf(err, t("pairing.shortCodeCancelError")));
  } finally {
    if (gen === shortCodeGen) {
      setPairing("shortCodeBusy", false);
    }
  }
}

export function clearExpiredPairingShortCode(): void {
  if (!pairing.shortCode) return;
  if (Date.now() >= pairing.shortCode.expiresAtMs) {
    setPairing("shortCode", null);
  }
}
