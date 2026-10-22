// web/src/store/pairing.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { pairingAPI, type PairingCode } from "../services/pairing";

export const [pairing, setPairing] = createStore<{
  code: PairingCode | null;
  loaded: boolean;
  busy: boolean;
  error: string;
}>({
  code: null,
  loaded: false,
  busy: false,
  error: "",
});

let pairingGen = 0;
let rotating = false;

// Fetches the current LAN pairing code. The QR is regenerated on every call so
// the displayed address follows the machine's network.
export async function loadPairingCode(): Promise<void> {
  if (rotating) return;
  const gen = ++pairingGen;
  setPairing({ busy: true, error: "" });
  try {
    const code = await pairingAPI.getCode();
    if (gen !== pairingGen) return;
    setPairing({ code, loaded: true, error: "" });
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
  setPairing({ busy: true, error: "" });
  try {
    const code = await pairingAPI.rotate();
    if (gen === pairingGen) {
      setPairing({ code, loaded: true, error: "" });
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
