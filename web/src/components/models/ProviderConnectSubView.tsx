// web/src/components/models/ProviderConnectSubView.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowLeft,
  Check,
  ExternalLink,
  LoaderCircle,
  Pencil,
} from "lucide-solid";
import { hoxiAPI, type ProviderAuthAuthorization, type ProviderInfo } from "../../services/api";
import {
  connectProviderWithKey,
  isCustomOrConfiguredProvider,
  loadProviders,
  openProviderModal,
  recordProbeTelemetry,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import ProviderQualityCard from "./ProviderQualityCard";
import { ProviderSelectAllView } from "./ProviderSelectAllView";

export { ProviderSelectAllView };

export function ProviderConnectView(props: {
  provider: ProviderInfo;
  connected: boolean;
  onBack: () => void;
}) {
  const initialBaseURL = () =>
    workspace.globalConfig?.provider?.[props.provider.id]?.options?.baseURL ||
    props.provider.options?.baseURL ||
    "";

  const [authMethodIndex, setAuthMethodIndex] = createSignal(0);
  const [apiKey, setApiKey] = createSignal("");
  const [baseURL, setBaseURL] = createSignal(initialBaseURL());
  const [extraModelID, setExtraModelID] = createSignal("");
  const [oauthState, setOauthState] = createSignal<ProviderAuthAuthorization | null>(null);
  const [oauthCode, setOauthCode] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [probingModel, setProbingModel] = createSignal(false);
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  const authMethods = createMemo(() => {
    const methods = workspace.providerAuthMethods[props.provider.id];
    if (Array.isArray(methods) && methods.length > 0) return methods;
    return [{ type: "api" as const, label: t("models.apiKey") }];
  });

  const onProbeExtraModel = async () => {
    const mid = extraModelID().trim();
    if (!mid || probingModel()) return true;
    setProbingModel(true);
    setFeedback(null);
    try {
      const res = await hoxiAPI.testProviderModel({
        providerID: props.provider.id,
        baseURL: baseURL().trim() || undefined,
        apiKey: apiKey().trim() || undefined,
        npm: props.provider.npm,
        modelID: mid,
      });
      recordProbeTelemetry(props.provider.id, res.ok, res.latencyMs, res.error, mid);
      if (!res.ok) {
        setFeedback({ ok: false, text: `${mid}: ${res.error || t("models.connectFailed")}` });
        return false;
      }
      setFeedback({ ok: true, text: `✓ ${mid} (${res.latencyMs ?? 0}ms)` });
      return true;
    } catch (err: any) {
      const errMsg = err?.message || t("models.connectFailed");
      recordProbeTelemetry(props.provider.id, false, undefined, errMsg, mid);
      setFeedback({ ok: false, text: errMsg });
      return false;
    } finally {
      setProbingModel(false);
    }
  };

  const onSaveConnect = async (ev: Event) => {
    ev.preventDefault();
    if (busy()) return;
    if (!apiKey().trim() && !baseURL().trim() && !extraModelID().trim()) {
      setFeedback({ ok: false, text: t("models.enterApiKeyOrCustomConfig") });
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      await connectProviderWithKey({
        providerID: props.provider.id,
        apiKey: apiKey(),
        baseURL: baseURL() || undefined,
        customModelID: extraModelID() || undefined,
      });
      setApiKey("");
      props.onBack();
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.connectFailed") });
    } finally {
      setBusy(false);
    }
  };

  const onStartOAuth = async () => {
    const pid = props.provider.id;
    setBusy(true);
    setFeedback(null);
    try {
      const res = await hoxiAPI.authorizeProviderOAuth(
        pid,
        authMethodIndex(),
        undefined,
        workspace.rootDir || undefined
      );
      setOauthState(res);
      if (res?.url) {
        window.open(res.url, "_blank", "noopener,noreferrer");
      }
      if (res?.method === "auto") {
        await hoxiAPI.completeProviderOAuth(
          pid,
          authMethodIndex(),
          undefined,
          workspace.rootDir || undefined
        );
        await hoxiAPI.disposeGlobal().catch(() => {});
        await loadProviders();
        props.onBack();
      }
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.oauthFailed") });
    } finally {
      setBusy(false);
    }
  };

  const onCompleteOAuthCode = async () => {
    const pid = props.provider.id;
    if (!oauthCode().trim()) return;
    setBusy(true);
    setFeedback(null);
    try {
      await hoxiAPI.completeProviderOAuth(
        pid,
        authMethodIndex(),
        oauthCode().trim(),
        workspace.rootDir || undefined
      );
      await hoxiAPI.disposeGlobal().catch(() => {});
      await loadProviders();
      props.onBack();
    } catch (err: any) {
      setFeedback({ ok: false, text: err?.message || t("models.oauthVerificationFailed") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSaveConnect} class="flex-1 flex flex-col justify-between p-6 overflow-y-auto">
      <div class="space-y-4">
        <div class="flex items-center justify-between">
          <button
            type="button"
            onClick={() => props.onBack()}
            class="inline-flex items-center gap-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <ArrowLeft size={14} />
            {t("models.tabCatalog")}
          </button>

          <Show when={isCustomOrConfiguredProvider(props.provider)}>
            <button
              type="button"
              onClick={() => openProviderModal("custom", props.provider.id)}
              class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] cursor-pointer"
            >
              <Pencil size={12} />
              {t("models.editCustomEndpoint")}
            </button>
          </Show>
        </div>

        <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 p-4">
          <ProviderIcon id={props.provider.id} class="w-6 h-6 shrink-0" />
          <div class="min-w-0 flex-1">
            <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
              {props.provider.name || props.provider.id}
            </h3>
            <p class="font-mono text-[11px] text-[var(--text-muted)]">
              ID: {props.provider.id} ·{" "}
              {t("models.modelsCount", {
                count: Object.keys(props.provider.models || {}).length,
              })}
            </p>
          </div>
        </div>

        <Show when={authMethods().length > 1}>
          <div class="inline-flex rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
            <For each={authMethods()}>
              {(m, idx) => (
                <button
                  type="button"
                  onClick={() => {
                    setAuthMethodIndex(idx());
                    setOauthState(null);
                  }}
                  class={`rounded-lg px-3 py-1 text-[11.5px] font-medium cursor-pointer ${
                    authMethodIndex() === idx()
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  {m.label}
                </button>
              )}
            </For>
          </div>
        </Show>

        <Show
          when={authMethods()[authMethodIndex()]?.type !== "oauth"}
          fallback={
            <div class="space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 p-4">
              <Show
                when={oauthState()}
                fallback={
                  <button
                    type="button"
                    disabled={busy()}
                    onClick={() => void onStartOAuth()}
                    class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 cursor-pointer"
                  >
                    <ExternalLink size={14} />
                    {authMethods()[authMethodIndex()]?.label || t("models.oauthLogin")}
                  </button>
                }
              >
                {(st) => (
                  <div class="space-y-3 text-[12.5px]">
                    <p class="text-[var(--text-secondary)]">{st().instructions}</p>
                    <Show when={st().method === "code"}>
                      <div class="flex gap-2">
                        <input
                          type="text"
                          placeholder={t("models.pasteAuthCodePlaceholder")}
                          value={oauthCode()}
                          onInput={(e) => setOauthCode(e.currentTarget.value)}
                          class="flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-[12.5px] text-[var(--text-primary)] outline-none"
                        />
                        <button
                          type="button"
                          onClick={() => void onCompleteOAuthCode()}
                          class="rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12px] font-medium text-white cursor-pointer"
                        >
                          {t("common.confirm")}
                        </button>
                      </div>
                    </Show>
                  </div>
                )}
              </Show>
            </div>
          }
        >
          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              {t("models.apiKey")}
            </label>
            <input
              type="password"
              placeholder={
                props.connected
                  ? t("models.apiKeyConnectedPlaceholder")
                  : t("models.apiKeyPlaceholder", {
                      provider: props.provider.name || props.provider.id,
                    })
              }
              value={apiKey()}
              onInput={(e) => setApiKey(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3.5 py-2.5 font-mono text-[12.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>

          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              {t("models.customBaseUrl")}{" "}
              <span class="text-[var(--text-faint)] font-normal">({t("models.optional")})</span>
            </label>
            <input
              type="text"
              placeholder={t("models.customBaseUrlHint")}
              value={baseURL()}
              onInput={(e) => setBaseURL(e.currentTarget.value)}
              class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
            />
          </div>

          <div>
            <label class="block text-[11.5px] font-medium text-[var(--text-secondary)] mb-1.5">
              {t("models.registerExtraModel")}{" "}
              <span class="text-[var(--text-faint)] font-normal">({t("models.optional")})</span>
            </label>
            <div class="flex items-center gap-2">
              <input
                type="text"
                autocapitalize="off"
                autocorrect="off"
                spellcheck={false}
                placeholder={t("models.extraModelPlaceholder")}
                value={extraModelID()}
                onInput={(e) => setExtraModelID(e.currentTarget.value)}
                class="flex-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3.5 py-2 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)] selectable-text"
              />
              <Show when={extraModelID().trim()}>
                <button
                  type="button"
                  disabled={probingModel()}
                  onClick={() => void onProbeExtraModel()}
                  class="shrink-0 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-2 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
                >
                  {probingModel() ? t("models.testing") : t("models.testConnection")}
                </button>
              </Show>
            </div>
          </div>
        </Show>

        <ProviderQualityCard providerID={props.provider.id} />

        <Show when={feedback()}>
          <div
            class={`rounded-xl border px-3.5 py-2.5 text-[12px] ${
              feedback()!.ok
                ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
            }`}
          >
            {feedback()!.text}
          </div>
        </Show>
      </div>

      <div class="flex items-center justify-between border-t border-[var(--border-subtle)] pt-4 mt-4">
        <span class="text-[11px] text-[var(--text-faint)]">{t("models.keysStoreNote")}</span>
        <button
          type="submit"
          disabled={busy()}
          class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[12.5px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
        >
          <Show when={!busy()} fallback={<LoaderCircle size={14} class="animate-spin" />}>
            <Check size={14} />
          </Show>
          {t("models.saveAndConnect")}
        </button>
      </div>
    </form>
  );
}
