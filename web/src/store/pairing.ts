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

// Fetches the current LAN pairing code. The QR is regenerated on every call so
// the displayed address follows the machine's network.
export async function loadPairingCode(): Promise<void> {
  if (pairing.busy) return;
  setPairing({ busy: true, error: "" });
  try {
    setPairing({ code: await pairingAPI.getCode(), loaded: true });
  } catch {
    setPairing({ loaded: true, error: t("pairing.loadError") });
  } finally {
    setPairing("busy", false);
  }
}
