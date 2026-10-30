import { hoxiAPI, authAPI } from "../services/api";
import { desktopAuth, logoutDesktopAccount, refreshAuthStatus, setDesktopAuth } from "../store/auth-state";
import {
  connectOfficialAinode,
  ensureOfficialProviderConnected,
  loadOfficialAinodeModels,
  resetOfficialAinodeState,
  officialAinodeState,
  setOfficialAinodeState,
} from "../store/provider-official";
import { loadProviders } from "../store/workspace-providers";
import { OFFICIAL_PROVIDER_ID, setWorkspace, workspace } from "../store/workspace-state";
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

  it("does not auto-connect while logged out", async () => {
    const orig = hoxiAPI.connectOfficialAinodeProvider;
    let called = false;
    try {
      setDesktopAuth({ isLoggedIn: false });
      hoxiAPI.connectOfficialAinodeProvider = async () => {
        called = true;
        return { authenticated: true, providerId: OFFICIAL_PROVIDER_ID, baseUrl: "", modelCount: 0, models: [] };
      };
      await ensureOfficialProviderConnected();
      assertEqual(called, false);
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = orig;
    }
  });

  it("refreshes the catalog instead of reprovisioning when already connected", async () => {
    const origConnect = hoxiAPI.connectOfficialAinodeProvider;
    const origModels = hoxiAPI.fetchOfficialAinodeModels;
    let connectCalled = false;
    try {
      setDesktopAuth({ isLoggedIn: true });
      setWorkspace("hoxiProviders", { [OFFICIAL_PROVIDER_ID]: { name: "Hoxi Official", models: {} } });
      setOfficialAinodeState({ loaded: false, loading: false, baseUrl: "", models: [], error: null });
      hoxiAPI.connectOfficialAinodeProvider = async () => {
        connectCalled = true;
        return { authenticated: true, providerId: OFFICIAL_PROVIDER_ID, baseUrl: "", modelCount: 0, models: [] };
      };
      hoxiAPI.fetchOfficialAinodeModels = async () => ({
        authenticated: true,
        gatewayAvailable: true,
        baseUrl: "https://api.ainode.run/v1",
        models: [{ id: "m1", name: "M1", inputPointsPerM: 1, outputPointsPerM: 2, perCallPoints: 0 }],
      });

      await ensureOfficialProviderConnected();

      assertEqual(connectCalled, false);
      assertEqual(officialAinodeState.models.length, 1);
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = origConnect;
      hoxiAPI.fetchOfficialAinodeModels = origModels;
      setWorkspace("hoxiProviders", {});
    }
  });

  it("preserves user-selected model on connect/reconnect and only auto-selects when no valid model is chosen", async () => {
    const origConnect = hoxiAPI.connectOfficialAinodeProvider;
    const origModels = hoxiAPI.fetchOfficialAinodeModels;
    const origGetHoxiModelsConfig = hoxiAPI.getHoxiModelsConfig;
    const origListProviders = hoxiAPI.listProviders;
    const prevModel = workspace.defaultAI.model;
    const prevProviders = workspace.providers;
    const prevHoxiProviders = workspace.hoxiProviders;
    const prevConnected = workspace.connectedProviders;
    try {
      setDesktopAuth({ isLoggedIn: true });
      hoxiAPI.connectOfficialAinodeProvider = async () => ({
        authenticated: true,
        providerId: OFFICIAL_PROVIDER_ID,
        baseUrl: "https://api.ainode.run/v1",
        modelCount: 1,
        firstModelId: "gpt-5.4",
        models: [{ id: "gpt-5.4", name: "GPT-5.4", inputPointsPerM: 25000, outputPointsPerM: 100000, perCallPoints: 0 }],
      });
      hoxiAPI.fetchOfficialAinodeModels = async () => ({
        authenticated: true,
        gatewayAvailable: true,
        baseUrl: "https://api.ainode.run/v1",
        models: [{ id: "gpt-5.4", name: "GPT-5.4", inputPointsPerM: 25000, outputPointsPerM: 100000, perCallPoints: 0 }],
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: {
          anthropic: { name: "Anthropic", models: { "claude-sonnet-4-6": { name: "Claude Sonnet 4.6" } } },
          [OFFICIAL_PROVIDER_ID]: { name: "Hoxi Official", models: { "gpt-5.4": { name: "GPT-5.4" } } },
        },
        openCodeProviders: [],
        configPath: ".hoxi/models.json",
      });
      hoxiAPI.listProviders = async () => ({
        all: [
          {
            id: "anthropic",
            name: "Anthropic",
            models: { "claude-sonnet-4-6": { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6" } } as Record<string, { id: string; name: string }>,
          },
          {
            id: OFFICIAL_PROVIDER_ID,
            name: "Hoxi Official",
            models: { "gpt-5.4": { id: "gpt-5.4", name: "GPT-5.4" } } as Record<string, { id: string; name: string }>,
          },
        ],
        connected: ["anthropic", OFFICIAL_PROVIDER_ID],
        default: {},
      });

      // Case 1: User already has a valid selected model -> must NOT overwrite it
      setWorkspace("defaultAI", "model", { providerID: "anthropic", modelID: "claude-sonnet-4-6" });
      await connectOfficialAinode();
      assertEqual(workspace.selectedModel?.providerID, "anthropic");
      assertEqual(workspace.selectedModel?.modelID, "claude-sonnet-4-6");

      // Case 2: No model is selected -> auto-selects firstModelId from official provider
      setWorkspace("defaultAI", "model", null);
      await connectOfficialAinode();
      assertEqual(workspace.selectedModel?.providerID, OFFICIAL_PROVIDER_ID);
      assertEqual(workspace.selectedModel?.modelID, "gpt-5.4");
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = origConnect;
      hoxiAPI.fetchOfficialAinodeModels = origModels;
      hoxiAPI.getHoxiModelsConfig = origGetHoxiModelsConfig;
      hoxiAPI.listProviders = origListProviders;
      setWorkspace({
        providers: prevProviders,
        hoxiProviders: prevHoxiProviders,
        connectedProviders: prevConnected,
      });
      setWorkspace("defaultAI", "model", prevModel);
    }
  });

  it("waits for in-flight loadProviders before deciding whether to provision official provider", async () => {
    const origConnect = hoxiAPI.connectOfficialAinodeProvider;
    const origModels = hoxiAPI.fetchOfficialAinodeModels;
    const origGetHoxiModelsConfig = hoxiAPI.getHoxiModelsConfig;
    const origListProviders = hoxiAPI.listProviders;
    const prevHoxiProviders = workspace.hoxiProviders;
    let connectCalled = false;
    let resolveProviders!: () => void;
    try {
      setDesktopAuth({ isLoggedIn: true });
      setWorkspace("hoxiProviders", {});
      hoxiAPI.getHoxiModelsConfig = () =>
        new Promise((resolve) => {
          resolveProviders = () =>
            resolve({
              hoxiProviders: {
                [OFFICIAL_PROVIDER_ID]: {
                  name: "Hoxi Official",
                  models: { "gpt-5.4": { name: "GPT-5.4" } },
                },
              },
              openCodeProviders: [],
              configPath: ".hoxi/models.json",
            });
        });
      hoxiAPI.listProviders = async () => ({ all: [], connected: [OFFICIAL_PROVIDER_ID], default: {} });
      hoxiAPI.connectOfficialAinodeProvider = async () => {
        connectCalled = true;
        return { authenticated: true, providerId: OFFICIAL_PROVIDER_ID, baseUrl: "", modelCount: 0, models: [] };
      };
      hoxiAPI.fetchOfficialAinodeModels = async () => ({
        authenticated: true,
        gatewayAvailable: true,
        baseUrl: "https://api.ainode.run/v1",
        models: [{ id: "gpt-5.4", name: "GPT-5.4", inputPointsPerM: 25000, outputPointsPerM: 100000, perCallPoints: 0 }],
      });

      const inFlightLoad = loadProviders();
      const ensurePromise = ensureOfficialProviderConnected();
      resolveProviders();
      await Promise.all([inFlightLoad, ensurePromise]);

      assertEqual(connectCalled, false);
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = origConnect;
      hoxiAPI.fetchOfficialAinodeModels = origModels;
      hoxiAPI.getHoxiModelsConfig = origGetHoxiModelsConfig;
      hoxiAPI.listProviders = origListProviders;
      setWorkspace("hoxiProviders", prevHoxiProviders);
    }
  });
});


describe("official provider account lifecycle", () => {
  it("discards a catalog response arriving after logout", async () => {
    const origModels = hoxiAPI.fetchOfficialAinodeModels;
    const origLogout = authAPI.logout;
    try {
      setDesktopAuth({ isLoggedIn: true });
      resetOfficialAinodeState();
      let resolveOld!: (value: Awaited<ReturnType<typeof origModels>>) => void;
      hoxiAPI.fetchOfficialAinodeModels = () => new Promise((resolve) => { resolveOld = resolve; });
      authAPI.logout = async () => ({ success: true });
      const pending = loadOfficialAinodeModels(true);
      await logoutDesktopAccount();
      resolveOld({ authenticated: true, gatewayAvailable: true, baseUrl: "https://old-account", models: [{ id: "old", name: "Old", inputPointsPerM: 1, outputPointsPerM: 1, perCallPoints: 0 }] });
      await pending;
      assertEqual(desktopAuth.isLoggedIn, false);
      assertEqual(officialAinodeState.loaded, false);
      assertEqual(officialAinodeState.models.length, 0);
    } finally {
      hoxiAPI.fetchOfficialAinodeModels = origModels;
      authAPI.logout = origLogout;
    }
  });

  it("discards a late connect before reloading providers or changing models", async () => {
    const origConnect = hoxiAPI.connectOfficialAinodeProvider;
    const origLogout = authAPI.logout;
    const origList = hoxiAPI.listProviders;
    let loads = 0;
    try {
      setDesktopAuth({ isLoggedIn: true });
      let resolveOld!: (value: Awaited<ReturnType<typeof origConnect>>) => void;
      hoxiAPI.connectOfficialAinodeProvider = () => new Promise((resolve) => { resolveOld = resolve; });
      hoxiAPI.listProviders = async () => { loads++; return { all: [], connected: [], default: {} }; };
      authAPI.logout = async () => ({ success: true });
      const pending = connectOfficialAinode().then(() => false, () => true);
      await logoutDesktopAccount();
      resolveOld({ authenticated: true, providerId: OFFICIAL_PROVIDER_ID, baseUrl: "", modelCount: 0, models: [] });
      assertEqual(await pending, true);
      assertEqual(loads, 0);
    } finally {
      hoxiAPI.connectOfficialAinodeProvider = origConnect;
      authAPI.logout = origLogout;
      hoxiAPI.listProviders = origList;
    }
  });

  it("keeps a new account catalog when an old account request arrives later", async () => {
    const origModels = hoxiAPI.fetchOfficialAinodeModels;
    const origStatus = authAPI.getStatus;
    const origMember = authAPI.getMemberStatus;
    try {
      setDesktopAuth({ isLoggedIn: true, user: { id: 1, email: "old@example.com", nickname: "Old" }, serverBaseUrl: "https://hoxi.ai" });
      resetOfficialAinodeState();
      let resolveOld!: (value: Awaited<ReturnType<typeof origModels>>) => void;
      hoxiAPI.fetchOfficialAinodeModels = () => new Promise((resolve) => { resolveOld = resolve; });
      const pending = loadOfficialAinodeModels(true);
      authAPI.getStatus = async () => ({ isLoggedIn: true, isAuthorizing: false, user: { id: 2, email: "new@example.com", nickname: "New" }, serverBaseUrl: "https://hoxi.ai" });
      authAPI.getMemberStatus = async () => ({ checkedInToday: false, streak: 0, maxStreak: 0, todayPoints: 0, nextPoints: 0, balance: 0 });
      await refreshAuthStatus();
      hoxiAPI.fetchOfficialAinodeModels = async () => ({ authenticated: true, gatewayAvailable: true, baseUrl: "https://new-account", models: [{ id: "new", name: "New", inputPointsPerM: 1, outputPointsPerM: 1, perCallPoints: 0 }] });
      await loadOfficialAinodeModels(true);
      resolveOld({ authenticated: true, gatewayAvailable: true, baseUrl: "https://old-account", models: [] });
      await pending;
      assertEqual(officialAinodeState.models[0]?.id, "new");
      assertEqual(officialAinodeState.baseUrl, "https://new-account");
    } finally {
      hoxiAPI.fetchOfficialAinodeModels = origModels;
      authAPI.getStatus = origStatus;
      authAPI.getMemberStatus = origMember;
    }
  });
});
