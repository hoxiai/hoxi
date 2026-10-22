import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import {
  activeDirectory,
  appSettings,
  openExternalURL,
  openFileInInspector,
  openImagePreview,
  openURLInInspector,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import { t } from "../../i18n";
import { Icon } from "../icons";
import { stripFileLineSuffix } from "./markdown-parser";

const MAX_LOCAL_IMAGE_CACHE = 40;
const MAX_CONCURRENT_LOCAL_IMAGES = 2;
const LOCAL_IMAGE_CACHE = new Map<string, string>();
const LOCAL_IMAGE_INFLIGHT = new Map<string, Promise<string | null>>();
const LOCAL_IMAGE_QUEUE: Array<() => void> = [];
let activeLocalImageLoads = 0;

function runLocalImageTask<T>(task: () => Promise<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const run = () => {
      activeLocalImageLoads++;
      Promise.resolve()
        .then(task)
        .then(resolve, reject)
        .finally(() => {
          activeLocalImageLoads--;
          LOCAL_IMAGE_QUEUE.shift()?.();
        });
    };
    if (activeLocalImageLoads < MAX_CONCURRENT_LOCAL_IMAGES) {
      run();
    } else {
      LOCAL_IMAGE_QUEUE.push(run);
    }
  });
}

function localImageKey(path: string, directory: string, sessionID?: string): string {
  return `${directory}\0${sessionID ?? ""}\0${path}`;
}

function cachedLocalImage(path: string, directory: string, sessionID?: string): string | undefined {
  const key = localImageKey(path, directory, sessionID);
  const hit = LOCAL_IMAGE_CACHE.get(key);
  if (!hit) return undefined;
  LOCAL_IMAGE_CACHE.delete(key);
  LOCAL_IMAGE_CACHE.set(key, hit);
  return hit;
}

function storeLocalImage(path: string, directory: string, sessionID: string | undefined, dataUrl: string) {
  const key = localImageKey(path, directory, sessionID);
  LOCAL_IMAGE_CACHE.delete(key);
  LOCAL_IMAGE_CACHE.set(key, dataUrl);
  if (LOCAL_IMAGE_CACHE.size > MAX_LOCAL_IMAGE_CACHE) {
    const oldest = LOCAL_IMAGE_CACHE.keys().next().value;
    if (oldest !== undefined) LOCAL_IMAGE_CACHE.delete(oldest);
  }
}

function loadLocalImage(
  path: string,
  directory: string,
  sessionID?: string
): Promise<string | null> {
  const key = localImageKey(path, directory, sessionID);
  const cached = cachedLocalImage(path, directory, sessionID);
  if (cached) return Promise.resolve(cached);
  const pending = LOCAL_IMAGE_INFLIGHT.get(key);
  if (pending) return pending;

  const request = runLocalImageTask(() =>
    hoxiAPI
      .readFileContent(path, directory || undefined, sessionID)
      .then((res) => {
        if (res.type !== "binary" || res.encoding !== "base64") return null;
        if (typeof res.size === "number" && res.size > 8 * 1024 * 1024) return null;
        const mime = (res.mimeType || "").toLowerCase();
        if (!mime.startsWith("image/") || mime.startsWith("image/svg")) return null;
        const dataUrl = `data:${mime};base64,${res.content}`;
        storeLocalImage(path, directory, sessionID, dataUrl);
        return dataUrl;
      })
      .catch(() => null)
  ).finally(() => {
    LOCAL_IMAGE_INFLIGHT.delete(key);
  });

  LOCAL_IMAGE_INFLIGHT.set(key, request);
  return request;
}

export default function MarkdownImage(props: { src: string; alt: string; title?: string }) {
  const [resolved, setResolved] = createSignal("");
  const [failed, setFailed] = createSignal(false);
  const [loading, setLoading] = createSignal(false);

  const rawSrc = () => (props.src || "").trim();
  const isRemote = () => /^https?:\/\//i.test(rawSrc());
  const isDataImage = () => /^data:image\/(?:png|jpe?g|gif|webp|avif);base64,/i.test(rawSrc());
  const isLocal = () =>
    !isRemote() &&
    !isDataImage() &&
    (!/^[a-z][a-z0-9+.-]*:/i.test(rawSrc()) || /^[a-zA-Z]:[\\/]/.test(rawSrc()));
  const localPath = () => (isLocal() ? stripFileLineSuffix(rawSrc()) : "");
  const canOpen = () => Boolean(localPath()) || isRemote() || isDataImage();
  const displaySrc = () => (isRemote() || isDataImage() ? rawSrc() : resolved());

  createEffect(() => {
    if (isRemote() || isDataImage()) {
      setResolved(rawSrc());
      setFailed(false);
      setLoading(false);
      return;
    }

    const path = localPath();
    if (!path) {
      setResolved("");
      setLoading(false);
      setFailed(true);
      return;
    }

    const directory = activeDirectory() || workspace.rootDir || "";
    const sessionID = workspace.activeSessionID || undefined;
    if (!workspace.connected && !directory) {
      setLoading(false);
      setFailed(true);
      return;
    }

    const cached = cachedLocalImage(path, directory, sessionID);
    if (cached) {
      setResolved(cached);
      setLoading(false);
      setFailed(false);
      return;
    }

    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });

    setLoading(true);
    setFailed(false);
    void loadLocalImage(path, directory, sessionID).then((url) => {
      if (cancelled) return;
      setLoading(false);
      if (url) {
        setResolved(url);
        setFailed(false);
      } else {
        setResolved("");
        setFailed(true);
      }
    });
  });

  const handleOpen = (e: MouseEvent) => {
    e.preventDefault();
    const path = localPath();
    if (path) {
      openImagePreview({
        src: displaySrc(),
        alt: props.alt || path,
        originalLabel: t("timeline.openInInspectorTooltip"),
        onOpenOriginal: () => openFileInInspector(path),
      });
      return;
    }
    if (!isRemote() && !isDataImage()) return;
    if (isDataImage()) {
      openImagePreview({ src: rawSrc(), alt: props.alt || "" });
      return;
    }
    const target =
      e.metaKey || e.ctrlKey
        ? appSettings.linkOpenTarget === "external"
          ? "inspector"
          : "external"
        : appSettings.linkOpenTarget;
    openImagePreview({
      src: rawSrc(),
      alt: props.alt || rawSrc(),
      originalLabel:
        target === "external"
          ? t("timeline.openInExternalBrowserTooltip")
          : t("timeline.openInBrowserTooltip"),
      onOpenOriginal: () =>
        target === "external" ? openExternalURL(rawSrc()) : openURLInInspector(rawSrc()),
    });
  };

  const openTitle = () => {
    if (localPath()) return t("timeline.openInInspectorTooltip");
    return appSettings.linkOpenTarget === "external"
      ? t("timeline.openInExternalBrowserTooltip")
      : t("timeline.openInBrowserTooltip");
  };

  return (
    <Show
      when={!failed() && displaySrc()}
      fallback={
        <span class="my-1 inline-flex max-w-full items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-2 text-[0.88em] text-[var(--text-muted)]">
          <Icon name="photo" size="small" class="shrink-0" />
          <span class="min-w-0 truncate">
            {loading()
              ? t("common.loading")
              : props.alt || localPath() || rawSrc() || t("timeline.imageLoadFailed")}
          </span>
        </span>
      }
    >
      <button
        type="button"
        disabled={!canOpen()}
        onClick={handleOpen}
        title={canOpen() ? openTitle() : undefined}
        class={`my-1 block max-w-full overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-left transition-colors ${
          canOpen()
            ? "cursor-zoom-in hover:border-[var(--accent-border)]"
            : "cursor-default"
        }`}
      >
        <img
          src={displaySrc()}
          alt={props.alt}
          title={props.title}
          loading="lazy"
          decoding="async"
          referrerpolicy="no-referrer"
          class="block max-h-[420px] max-w-full object-contain"
          onError={() => setFailed(true)}
        />
      </button>
    </Show>
  );
}
