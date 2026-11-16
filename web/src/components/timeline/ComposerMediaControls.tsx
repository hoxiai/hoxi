// web/src/components/timeline/ComposerMediaControls.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { LoaderCircle, Mic, Square } from "lucide-solid";
import { locale, t } from "../../i18n";
import {
  hasConfiguredAudioTranscriber,
  IMAGE_ASPECT_RATIOS,
  IMAGE_SIZE_TIERS,
  isAutoModel,
  isImageGenerationModel,
  refreshMediaCapabilities,
  selectedImageAspectRatio,
  selectedImageSize,
  sessionAIConfig,
  setSelectedImageAspectRatio,
  setSelectedImageSize,
  transcribeComposerAudio,
  workspace,
} from "../../store/workspace";

export function ComposerImageOptionsBar(props: { sessionID: string | null }) {
  const activeImageModel = createMemo(() => {
    const cfg = sessionAIConfig(props.sessionID);
    const model = cfg.model || workspace.selectedModel;
    if (!model || isAutoModel(model)) return false;
    return isImageGenerationModel(model.providerID, model.modelID);
  });

  return (
    <Show when={activeImageModel()}>
      <div class="inline-flex items-center gap-1.5 flex-wrap">
        <div
          class="inline-flex items-center gap-0.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5"
          title={t("timeline.imageAspectRatioLabel")}
        >
          <For each={IMAGE_ASPECT_RATIOS}>
            {(ratio) => (
              <button
                type="button"
                onClick={() => setSelectedImageAspectRatio(ratio)}
                class={`rounded-md px-1.5 py-0.5 font-mono text-[11px] transition-colors cursor-pointer ${
                  selectedImageAspectRatio() === ratio
                    ? "bg-[var(--bg-elevated)] font-semibold text-[var(--accent-primary)] shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                {ratio}
              </button>
            )}
          </For>
        </div>

        <div
          class="inline-flex items-center gap-0.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5"
          title={t("timeline.imageResolutionLabel")}
        >
          <For each={IMAGE_SIZE_TIERS}>
            {(tier) => (
              <button
                type="button"
                onClick={() => setSelectedImageSize(tier)}
                class={`rounded-md px-1.5 py-0.5 font-mono text-[11px] transition-colors cursor-pointer ${
                  selectedImageSize() === tier
                    ? "bg-[var(--bg-elevated)] font-semibold text-[var(--accent-primary)] shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                {tier}
              </button>
            )}
          </For>
        </div>
      </div>
    </Show>
  );
}

export function ComposerVoiceInputButton(props: {
  disabled?: boolean;
  onInsertText?: (text: string) => void;
  onNotice?: (message: string) => void;
}) {
  const [recording, setRecording] = createSignal(false);
  const [transcribing, setTranscribing] = createSignal(false);
  let recorder: MediaRecorder | null = null;
  let stream: MediaStream | null = null;
  let chunks: BlobPart[] = [];

  createEffect(() => {
    void workspace.providers.length;
    void refreshMediaCapabilities();
  });

  const stopTracks = () => {
    if (stream) {
      for (const track of stream.getTracks()) {
        track.stop();
      }
      stream = null;
    }
    recorder = null;
  };

  onCleanup(() => {
    if (recorder && recorder.state !== "inactive") {
      try {
        recorder.stop();
      } catch {
        /* ignore cleanup stop errors */
      }
    }
    stopTracks();
  });

  const canUseASR = createMemo(() => hasConfiguredAudioTranscriber());

  const pickRecorderMime = (): string => {
    if (typeof MediaRecorder === "undefined" || typeof MediaRecorder.isTypeSupported !== "function") {
      return "";
    }
    for (const candidate of [
      "audio/webm;codecs=opus",
      "audio/webm",
      "audio/mp4",
      "audio/ogg;codecs=opus",
    ]) {
      if (MediaRecorder.isTypeSupported(candidate)) return candidate;
    }
    return "";
  };

  const toggleRecording = async () => {
    if (transcribing() || props.disabled) return;
    if (recording()) {
      if (recorder && recorder.state !== "inactive") {
        recorder.stop();
      }
      return;
    }
    if (
      typeof navigator === "undefined" ||
      !navigator.mediaDevices?.getUserMedia ||
      typeof MediaRecorder === "undefined"
    ) {
      props.onNotice?.(t("timeline.voiceInputUnsupported"));
      return;
    }
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mimeType = pickRecorderMime();
      chunks = [];
      const mr = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      recorder = mr;
      mr.ondataavailable = (ev) => {
        if (ev.data && ev.data.size > 0) chunks.push(ev.data);
      };
      mr.onstop = () => {
        const recordedMime = mr.mimeType || mimeType || "audio/webm";
        const blob = new Blob(chunks, { type: recordedMime });
        chunks = [];
        stopTracks();
        setRecording(false);
        if (blob.size < 64) return;
        setTranscribing(true);
        void transcribeComposerAudio(blob, locale())
          .then((transcript) => {
            if (!transcript) {
              props.onNotice?.(t("timeline.voiceInputEmpty"));
              return;
            }
            props.onInsertText?.(transcript);
          })
          .catch((err) => {
            const msg = err instanceof Error ? err.message : String(err || "error");
            props.onNotice?.(t("timeline.voiceInputFailed", { error: msg }));
          })
          .finally(() => {
            setTranscribing(false);
          });
      };
      mr.start();
      setRecording(true);
    } catch (err) {
      stopTracks();
      setRecording(false);
      const msg = err instanceof Error ? err.message : String(err || "error");
      props.onNotice?.(t("timeline.voiceInputFailed", { error: msg }));
    }
  };

  return (
    <Show when={canUseASR()}>
      <button
        type="button"
        disabled={Boolean(props.disabled) || transcribing()}
        onClick={() => void toggleRecording()}
        title={
          transcribing()
            ? t("timeline.voiceInputTranscribing")
            : recording()
            ? t("timeline.voiceInputStopTooltip")
            : t("timeline.voiceInputTooltip")
        }
        class={`inline-flex h-7 items-center justify-center gap-1 rounded-full px-2 text-[11px] transition-colors cursor-pointer disabled:opacity-45 ${
          recording()
            ? "bg-[var(--danger-surface)] text-[var(--danger-text)] ring-1 ring-[var(--danger-border)]"
            : "bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
        }`}
      >
        <Show
          when={!transcribing()}
          fallback={<LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />}
        >
          <Show when={!recording()} fallback={<Square size={10} class="fill-current animate-pulse" />}>
            <Mic size={13.5} />
          </Show>
        </Show>
      </button>
    </Show>
  );
}
