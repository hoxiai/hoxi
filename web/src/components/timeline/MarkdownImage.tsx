import { createEffect, createSignal, onCleanup, onMount, Show } from "solid-js";
import {
  activeDirectory,
  appSettings,
  isAnyPortalMode,
  openAttachmentContextMenu,
  openExternalURL,
  openFileInInspector,
  openImagePreview,
  openURLInInspector,
  workspace,
} from "../../store/workspace";
import { getCachedMarkdownImage, loadMarkdownImage } from "../../store/markdown-images";
import { t } from "../../i18n";
import { Icon } from "../icons";
import { resolveMarkdownRelativePath, stripFileLineSuffix } from "./markdown-parser";
import { isSafePortalImageSrc } from "./rich-view-spec";
import { useMarkdownBaseFilePath, useMarkdownPortalMode } from "./stream-reveal";

export default function MarkdownImage(props: { src: string; alt: string; title?: string }) {
  let host!: HTMLSpanElement;
  const [nearViewport, setNearViewport] = createSignal(false);
  const [resolved, setResolved] = createSignal("");
  const [failed, setFailed] = createSignal(false);
  const [loading, setLoading] = createSignal(false);
  const baseFilePath = useMarkdownBaseFilePath();
  const markdownPortal = useMarkdownPortalMode();
  const isPortal = () => isAnyPortalMode() || markdownPortal();

  onMount(() => {
    if (typeof IntersectionObserver === "undefined") {
      setNearViewport(true);
      return;
    }
    const observer = new IntersectionObserver((entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      setNearViewport(true);
      observer.disconnect();
    }, { rootMargin: "600px" });
    observer.observe(host);
    onCleanup(() => observer.disconnect());
  });

  const rawSrc = () => (props.src || "").trim();
  const isRemote = () => /^https?:\/\//i.test(rawSrc());
  const isGatewayAttachment = () =>
    isPortal()
      ? isSafePortalImageSrc(rawSrc()) && !isRemote() && !/^data:image\//i.test(rawSrc())
      : /^\/api\/hoxi\/(?:attachments|[a-z0-9-]+\/portal\/attachments|[a-z0-9-]+\/[a-z0-9-]+\/[a-z0-9_-]+\/proxy\/attachments)\//i.test(rawSrc());
  const isDataImage = () =>
    isPortal()
      ? /^data:image\/(?:png|jpe?g|gif|webp|avif);base64,/i.test(rawSrc())
      : /^data:image\/(?:png|jpe?g|gif|webp|avif|svg\+xml)(?:;charset=[^;,]+)?;(?:base64|utf8),/i.test(rawSrc()) ||
        /^data:image\/svg\+xml,/i.test(rawSrc());
  const isDirectImage = () =>
    isPortal() ? isSafePortalImageSrc(rawSrc()) : isRemote() || isGatewayAttachment() || isDataImage();
  const isLocal = () =>
    !isDirectImage() &&
    (!/^[a-z][a-z0-9+.-]*:/i.test(rawSrc()) || /^[a-zA-Z]:[\\/]/.test(rawSrc()));
  const rawLocalPath = () => (isLocal() ? stripFileLineSuffix(rawSrc()) : "");
  const localPath = () => resolveMarkdownRelativePath(rawLocalPath(), baseFilePath());
  const canOpen = () => Boolean(localPath()) || isDirectImage();
  const displaySrc = () => (isDirectImage() ? rawSrc() : resolved());

  createEffect(() => {
    if (isDirectImage()) {
      setResolved(rawSrc());
      setFailed(false);
      setLoading(false);
      return;
    }

    const path = localPath();
    const fallbackPath = rawLocalPath();
    if (!path) {
      setResolved("");
      setLoading(false);
      setFailed(true);
      return;
    }

    const directory = activeDirectory() || workspace.rootDir || "";
    const sessionID = workspace.activeSessionID || undefined;
    if (isPortal() || (!workspace.connected && !directory)) {
      setLoading(false);
      setFailed(true);
      return;
    }

    const cached = getCachedMarkdownImage(path, directory, sessionID);
    if (cached) {
      setResolved(cached);
      setLoading(false);
      setFailed(false);
      return;
    }

    setResolved("");
    setFailed(false);
    if (!nearViewport()) {
      setLoading(true);
      return;
    }

    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });

    setLoading(true);
    setFailed(false);
    void loadMarkdownImage(path, directory, sessionID)
      .then((url) => {
        if (cancelled || url || !fallbackPath || fallbackPath === path) return url;
        return loadMarkdownImage(fallbackPath, directory, sessionID);
      })
      .then((url) => {
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

  const handleOpen = (e?: MouseEvent) => {
    e?.preventDefault();
    const portalMode = isPortal();
    const path = localPath();
    if (path && !portalMode) {
      openImagePreview({
        src: displaySrc(),
        alt: props.alt || path,
        localPath: path,
        originalLabel: t("timeline.openInInspectorTooltip"),
        onOpenOriginal: () => openFileInInspector(path),
      });
      return;
    }
    if (!isDirectImage()) return;
    if (isDataImage() || isGatewayAttachment()) {
      openImagePreview({ src: rawSrc(), alt: props.alt || "" });
      return;
    }
    const target = portalMode
      ? "external"
      : e && (e.metaKey || e.ctrlKey)
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

  const handleContextMenu = (e: MouseEvent) => {
    const src = displaySrc();
    if (!src) return;
    const path = localPath();
    const portalMode = isPortal();
    const remoteTarget =
      portalMode || appSettings.linkOpenTarget === "external" ? "external" : "inspector";
    openAttachmentContextMenu(e, {
      kind: "image",
      src,
      localPath: path || undefined,
      filename: props.alt || undefined,
      onPreview: canOpen() ? () => handleOpen() : undefined,
      originalLabel: path
        ? t("timeline.openInInspectorTooltip")
        : isRemote()
          ? remoteTarget === "external"
            ? t("timeline.openInExternalBrowserDirectTooltip")
            : t("timeline.openInBrowserTooltip")
          : undefined,
      onOpenOriginal:
        path && !portalMode
          ? () => openFileInInspector(path)
          : isRemote()
            ? () =>
                remoteTarget === "external"
                  ? openExternalURL(rawSrc())
                  : openURLInInspector(rawSrc())
            : undefined,
    });
  };

  const openTitle = () => {
    if (isPortal()) {
      return isRemote() ? t("timeline.openInExternalBrowserTooltip") : undefined;
    }
    if (localPath()) return t("timeline.openInInspectorTooltip");
    return appSettings.linkOpenTarget === "external"
      ? t("timeline.openInExternalBrowserTooltip")
      : t("timeline.openInBrowserTooltip");
  };

  return (
    <span ref={host} class="block min-h-8">
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
        onContextMenu={handleContextMenu}
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
    </span>
  );
}
