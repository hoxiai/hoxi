// web/src/services/provider-api.ts
import type {
  CandidateBreakerSnapshot,
  CandidateOutcomeInput,
  CodeAuditRequest,
  CodeAuditResult,
  ProviderAuthAuthorization,
  ProviderAuthMethod,
  ProviderListResponse,
  ProviderTelemetryStats,
} from "./api-types";
import { requestJSON, withDir } from "./http";
import { officialChannelAPI, providerMcpOAuthAPI } from "./platform-api";

export const providerAndAssistAPI = {
  // Providers, Models & Auth (Direct OpenCode endpoints used by Kilo)
  listProviders(directory?: string): Promise<ProviderListResponse> {
    return requestJSON(withDir("/provider", directory));
  },
  getProviderAuthMethods(directory?: string): Promise<Record<string, ProviderAuthMethod[]>> {
    return requestJSON(withDir("/provider/auth", directory));
  },
  authorizeProviderOAuth(
    providerID: string,
    method: number,
    inputs?: Record<string, string>,
    directory?: string
  ): Promise<ProviderAuthAuthorization> {
    return requestJSON(withDir(`/provider/${encodeURIComponent(providerID)}/oauth/authorize`, directory), {
      method: "POST",
      body: JSON.stringify({ method, ...(inputs ? { inputs } : {}) }),
    });
  },
  completeProviderOAuth(providerID: string, method: number, code?: string, directory?: string): Promise<boolean> {
    return requestJSON(withDir(`/provider/${encodeURIComponent(providerID)}/oauth/callback`, directory), {
      method: "POST",
      body: JSON.stringify({ method, ...(code ? { code } : {}) }),
    });
  },
  setProviderAuth(providerID: string, apiKey: string, metadata?: Record<string, string>): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, {
      method: "PUT",
      body: JSON.stringify({
        type: "api",
        key: apiKey.trim(),
        ...(metadata && Object.keys(metadata).length > 0 ? { metadata } : {}),
      }),
    });
  },
  removeProviderAuth(providerID: string): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, { method: "DELETE" });
  },
  fetchCustomProviderModels(opts: {
    baseURL: string;
    apiKey?: string;
    providerID?: string;
    headers?: Record<string, string>;
    directory?: string;
  }): Promise<{ ok?: boolean; models?: Array<{ id: string; name: string }>; error?: string; auth?: boolean }> {
    return requestJSON("/api/hoxi/provider/fetch-models", { method: "POST", body: JSON.stringify(opts) });
  },
  startProviderMcpOAuth: providerMcpOAuthAPI.start,
  pollProviderMcpOAuth: providerMcpOAuthAPI.poll,
  fetchOfficialChannelModels: officialChannelAPI.models,
  connectOfficialChannelProvider: officialChannelAPI.connect,
  getGlobalConfig(): Promise<Record<string, any>> {
    return requestJSON("/global/config");
  },
  updateGlobalConfig(config: Record<string, any>): Promise<Record<string, any>> {
    return requestJSON("/global/config", { method: "PATCH", body: JSON.stringify(config) });
  },
  getHoxiModelsConfig(): Promise<{
    hoxiProviders: Record<string, any>;
    openCodeProviders: string[];
    configPath: string;
  }> {
    return requestJSON("/api/hoxi/models-config");
  },
  saveHoxiProviderConfig(
    providerID: string,
    config: Record<string, any>
  ): Promise<{ ok: boolean; hoxiProviders: Record<string, any>; configPath: string }> {
    return requestJSON("/api/hoxi/models-config", {
      method: "POST",
      body: JSON.stringify({ providerID, config }),
    });
  },
  deleteHoxiProviderConfig(providerID: string): Promise<{ ok: boolean; hoxiProviders: Record<string, any> }> {
    return requestJSON(`/api/hoxi/models-config?providerID=${encodeURIComponent(providerID)}`, {
      method: "DELETE",
    });
  },
  getAutoPoolsConfig(): Promise<{
    ok: boolean;
    autoPools: Record<string, any>;
    keyCounts?: Record<string, number>;
  }> {
    return requestJSON("/api/hoxi/provider/auto-pools");
  },
  saveAutoPoolConfig(
    poolID: string,
    pool: Record<string, any>
  ): Promise<{ ok: boolean; autoPools: Record<string, any> }> {
    return requestJSON("/api/hoxi/provider/auto-pools", {
      method: "POST",
      body: JSON.stringify({ poolID, pool }),
    });
  },
  deleteAutoPoolConfig(poolID: string): Promise<{ ok: boolean; autoPools: Record<string, any> }> {
    return requestJSON(`/api/hoxi/provider/auto-pools?poolID=${encodeURIComponent(poolID)}`, {
      method: "DELETE",
    });
  },
  disposeGlobal(): Promise<void> {
    return requestJSON("/global/dispose", { method: "POST", body: JSON.stringify({}) });
  },
  testProviderModel(opts: {
    providerID?: string;
    baseURL?: string;
    apiKey?: string;
    npm?: string;
    modelID: string;
    headers?: Record<string, string>;
  }): Promise<{ ok: boolean; latencyMs?: number; error?: string }> {
    return requestJSON("/api/hoxi/provider/test-model", { method: "POST", body: JSON.stringify(opts) });
  },
  getProviderTelemetry(): Promise<{ providers: Record<string, ProviderTelemetryStats> }> {
    return requestJSON("/api/hoxi/provider/telemetry");
  },
  saveProviderTelemetry(providers: Record<string, ProviderTelemetryStats>): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/provider/telemetry", {
      method: "POST",
      body: JSON.stringify({ providers }),
    });
  },
  clearProviderTelemetry(providerID?: string): Promise<{ ok: boolean }> {
    return requestJSON(
      `/api/hoxi/provider/telemetry${providerID ? `?providerID=${encodeURIComponent(providerID)}` : ""}`,
      { method: "DELETE" }
    );
  },
  getProviderCandidates(): Promise<{ ok: boolean; breakers: Record<string, CandidateBreakerSnapshot> }> {
    return requestJSON("/api/hoxi/provider/candidates");
  },
  reportCandidateOutcome(input: CandidateOutcomeInput): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/provider/candidate-outcome", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  clearProviderCandidates(
    providerID?: string,
    modelID?: string
  ): Promise<{ ok: boolean; breakers: Record<string, CandidateBreakerSnapshot> }> {
    const params = new URLSearchParams();
    if (providerID) params.set("providerID", providerID);
    if (modelID) params.set("modelID", modelID);
    const qs = params.toString();
    return requestJSON(`/api/hoxi/provider/candidates${qs ? `?${qs}` : ""}`, { method: "DELETE" });
  },
  auditProviderCode(req: CodeAuditRequest): Promise<CodeAuditResult> {
    return requestJSON("/api/hoxi/provider/audit", { method: "POST", body: JSON.stringify(req) });
  },
  summarizeSessionTitle(req: {
    providerID: string;
    modelID: string;
    baseURL?: string;
    npm?: string;
    prompt: string;
    locale: string;
  }): Promise<{ ok: boolean; title: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/summarize-title", {
      method: "POST",
      body: JSON.stringify(req),
    });
  },
  generateCommitMessage(req: {
    providerID: string;
    modelID: string;
    baseURL?: string;
    npm?: string;
    sessionDigest?: string;
    sessionTitle?: string;
    lastPrompt?: string;
    diffSummary: string;
    locale: string;
  }): Promise<{ ok: boolean; message: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/generate-commit-message", {
      method: "POST",
      body: JSON.stringify(req),
    });
  },
  enhancePrompt(req: {
    providerID: string;
    modelID: string;
    baseURL?: string;
    npm?: string;
    prompt: string;
    locale: string;
  }): Promise<{ ok: boolean; prompt: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/enhance-prompt", {
      method: "POST",
      body: JSON.stringify(req),
    });
  },
};
