import { hoxiAPI } from "../services/api";
import { setDesktopAuth } from "../store/auth-state";
import {
  connectOfficialAinode,
  loadOfficialAinodeModels,
  officialAinodeState,
  setOfficialAinodeState,
} from "../store/workspace-providers";
import { assertEqual, describe, it } from "./test-harness";

describe("official AINode account connect", () => {
  it("clears the model catalog when logged out", async () => {
    setDesktopAuth({ isLoggedIn: false });
    setOfficialAinodeState({
      loaded: true,
      loading: false,
      baseUrl: "https://api.ainode.run/v1",
      models: [{ id: "stale", name: "Stale", inputPointsPerM: 1, outputPointsPerM: 2, perCallPoints: 0 }],
      error: null,
    });

    await loadOfficialAinodeModels(true);

    assertEqual(officialAinodeState.models.length, 0);
    assertEqual(officialAinodeState.loaded, false);
  });

  it("loads authorized models for the signed-in account", async () => {
    const orig = hoxiAPI.fetchOfficialAinodeModels;
    try {
      setDesktopAuth({ isLoggedIn: true });
      setOfficialAinodeState({ loaded: false, loading: false, baseUrl: "", models: [], error: null });
      hoxiAPI.fetchOfficialAinodeModels = async () => ({
        authenticated: true,
        gatewayAvailable: true,
        baseUrl: "https://api.ainode.run/v1",
        models: [
          { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", inputPointsPerM: 30000, outputPointsPerM: 150000, perCallPoints: 0 },
        ],
      });

      await loadOfficialAinodeModels(true);

      assertEqual(officialAinodeState.loaded, true);
      assertEqual(officialAinodeState.models.length, 1);
      assertEqual(officialAinodeState.models[0].id, "claude-sonnet-4-6");
      assertEqual(officialAinodeState.baseUrl, "https://api.ainode.run/v1");
    } finally {
      hoxiAPI.fetchOfficialAinodeModels = orig;
    }
  });

  it("refuses to connect when the account is not authenticated", async () => {
    const orig = hoxiAPI.connectOfficialAinodeProvider;
    try {
      setDesktopAuth({ isLoggedIn: true });
      hoxiAPI.connectOfficialAinodeProvider = async () => ({
        authenticated: false,
        providerId: "hoxi-official",
        baseUrl: "",
        modelCount: 0,
        models: [],
      });

      let threw = false;
      try {
        await connectOfficialAinode();
      } catch {
        threw = true;
      }
      assertEqual(threw, true);
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = orig;
    }
  });
});
