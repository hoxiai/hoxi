// web/src/components/models/ModelProbeControl.tsx
import { createSignal, Show } from "solid-js";
import { LoaderCircle, Radar } from "lucide-solid";
import {
  AUTO_PROVIDER_ID,
  probeProviderModel,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

function probeRecord(providerID: string, modelID: string) {
  return workspace.providerTelemetry[providerID]?.models?.[modelID];
}

function formatProbeLatency(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`;
}

export function ModelProbeBadge(props: { providerID: string; modelID: string }) {
  const record = () => probeRecord(props.providerID, props.modelID);
  const latency = () => record()?.lastProbeLatencyMs ?? 0;

  return (
    <>
      <Show when={record()?.lastProbeOk === false}>
        <span
          class="shrink-0 inline-flex items-center rounded border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-[1px] text-[11px] font-semibold text-[var(--danger-text)]"
          title={t("models.modelUnavailableHint")}
        >
          {t("models.modelUnavailable")}
        </span>
      </Show>
      <Show when={record()?.lastProbeOk === true && latency() > 0}>
        <span
          class="shrink-0 inline-flex items-center rounded border border-[var(--ok-border)] bg-[var(--ok-surface)] px-1.5 py-[1px] font-mono text-[11px] font-semibold text-[var(--ok-text)]"
          title={t("models.probeOkHint", { latency: formatProbeLatency(latency()) })}
        >
          {formatProbeLatency(latency())}
        </span>
      </Show>
    </>
  );
}

export function ModelProbeButton(props: {
  providerID: string;
  modelID: string;
  modelName: string;
  source?: string;
}) {
  const [probing, setProbing] = createSignal(false);
  const [probeError, setProbeError] = createSignal<string | null>(null);
  const canProbe = () =>
    props.providerID !== AUTO_PROVIDER_ID && props.source !== "runner";
  const unavailable = () => probeRecord(props.providerID, props.modelID)?.lastProbeOk === false;

  const runProbe = async () => {
    if (probing()) return;
    setProbing(true);
    setProbeError(null);
    try {
      const res = await probeProviderModel({
        providerID: props.providerID,
        modelID: props.modelID,
      });
      if (!res.ok) setProbeError(res.error || t("models.probeFailed"));
    } catch (err: any) {
      setProbeError(err?.message || t("models.probeFailed"));
    } finally {
      setProbing(false);
    }
  };

  return (
    <Show when={canProbe()}>
      <button
        type="button"
        aria-label={`${t("models.probeModel")}: ${props.modelName}`}
        title={probing() ? t("models.probingModel") : probeError() || t("models.probeModel")}
        disabled={probing()}
        onClick={(e) => {
          e.stopPropagation();
          void runProbe();
        }}
        class={`rounded-md p-1 transition-opacity focus-visible:outline focus-visible:outline-[var(--accent-primary)] cursor-pointer disabled:cursor-wait ${
          unavailable()
            ? "text-[var(--danger-text)] opacity-100"
            : "text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--accent-primary)]"
        } group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 ${
          unavailable() ? "opacity-100" : "opacity-0"
        }`}
      >
        <Show when={probing()} fallback={<Radar size={13} />}>
          <LoaderCircle size={13} class="animate-spin" />
        </Show>
      </button>
    </Show>
  );
}
