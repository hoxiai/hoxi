// web/src/components/preview/AttachmentContextMenu.tsx
// Global context menu and transient action toast for conversation attachments
// (images, files, audio, video) and fullscreen image previews.
import { createEffect, createMemo, createSignal, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import {
  AlertCircle,
  Check,
  Copy,
  Download,
  ExternalLink,
  FileText,
  FolderOpen,
  Link2,
  ZoomIn,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  attachmentActionFeedback,
  attachmentContextMenu,
  closeAttachmentContextMenu,
  closeImagePreview,
  copyAttachmentTextValue,
  copyImageSourceToClipboard,
  downloadAttachmentTarget,
  openAttachmentTargetExternally,
  openAttachmentTargetInInspector,
  resolveAttachmentMenuActions,
  showAttachmentActionFeedback,
  type AttachmentContextMenuState,
} from "../../store/workspace";

function downloadLabelForKind(kind: AttachmentContextMenuState["target"]["kind"]): string {
  switch (kind) {
    case "image":
      return t("timeline.downloadImage");
    case "audio":
      return t("timeline.downloadAudio");
    case "video":
      return t("timeline.downloadVideo");
    default:
      return t("timeline.downloadFile");
  }
}

function copyMetaLabel(kind: "link" | "path" | "filename"): string {
  switch (kind) {
    case "link":
      return t("timeline.copyImageLink");
    case "path":
      return t("timeline.copyFilePath");
    default:
      return t("timeline.copyFilename");
  }
}

function copiedMetaLabel(kind: "link" | "path" | "filename"): string {
  switch (kind) {
    case "link":
      return t("timeline.copiedImageLink");
    case "path":
      return t("timeline.copiedFilePath");
    default:
      return t("timeline.copiedFilename");
  }
}

function AttachmentContextMenuPopup(props: { menu: AttachmentContextMenuState }) {
  let root!: HTMLDivElement;
  const [position, setPosition] = createSignal({
    left: props.menu.clientX,
    top: props.menu.clientY,
  });

  const actions = createMemo(() => resolveAttachmentMenuActions(props.menu.target));
  const isImage = () => props.menu.target.kind === "image";

  const clampPosition = () => {
    if (!root) return;
    const bounds = root.getBoundingClientRect();
    const margin = 6;
    const left = Math.max(
      margin,
      Math.min(props.menu.clientX, window.innerWidth - bounds.width - margin)
    );
    const top = Math.max(
      margin,
      Math.min(props.menu.clientY, window.innerHeight - bounds.height - margin)
    );
    setPosition({ left, top });
  };

  createEffect(() => {
    void props.menu.clientX;
    void props.menu.clientY;
    void props.menu.target;
    queueMicrotask(clampPosition);
  });

  onMount(() => {
    clampPosition();
    root.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => {
      if (!root.contains(event.target as Node)) closeAttachmentContextMenu();
    };
    const dismiss = () => closeAttachmentContextMenu();
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("scroll", dismiss, true);
    window.addEventListener("blur", dismiss);
    window.addEventListener("resize", dismiss);
    onCleanup(() => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("scroll", dismiss, true);
      window.removeEventListener("blur", dismiss);
      window.removeEventListener("resize", dismiss);
    });
  });

  function onKeyDown(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === "Escape" || event.key === "Tab") {
      event.preventDefault();
      closeAttachmentContextMenu();
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const buttons = Array.from(root.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
    if (buttons.length === 0) return;
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? buttons.length - 1
          : event.key === "ArrowDown"
            ? (index + 1) % buttons.length
            : (index <= 0 ? buttons.length : index) - 1;
    buttons[next]?.focus();
  }

  const handleCopyImage = () => {
    const src = props.menu.target.src;
    const promise = copyImageSourceToClipboard(src);
    closeAttachmentContextMenu();
    void promise.then((ok) => {
      showAttachmentActionFeedback(
        ok ? t("timeline.copiedImage") : t("timeline.copyImageFailed"),
        ok ? "ok" : "error"
      );
    });
  };

  const handleDownload = () => {
    const target = props.menu.target;
    closeAttachmentContextMenu();
    void downloadAttachmentTarget(target).then((ok) => {
      if (!ok) {
        showAttachmentActionFeedback(t("timeline.downloadFailed"), "error");
      }
    });
  };

  const handlePreview = () => {
    const preview = props.menu.target.onPreview;
    closeAttachmentContextMenu();
    preview?.();
  };

  const handleOpenInInspector = () => {
    const target = props.menu.target;
    closeAttachmentContextMenu();
    if (openAttachmentTargetInInspector(target)) {
      closeImagePreview();
    }
  };

  const handleOpenExternal = (action: "open" | "reveal") => {
    const target = props.menu.target;
    closeAttachmentContextMenu();
    void openAttachmentTargetExternally(target, action).then((ok) => {
      if (!ok) {
        showAttachmentActionFeedback(t("timeline.openExternalFailed"), "error");
      }
    });
  };

  const handleCopyMeta = () => {
    const meta = actions().copyMeta;
    if (!meta) return;
    const promise = copyAttachmentTextValue(meta.value);
    closeAttachmentContextMenu();
    void promise.then((ok) => {
      showAttachmentActionFeedback(
        ok ? copiedMetaLabel(meta.kind) : t("timeline.copyTextFailed"),
        ok ? "ok" : "error"
      );
    });
  };

  const hasPrimary = () =>
    actions().canCopyImage ||
    actions().canDownload ||
    actions().canPreview ||
    (!isImage() && actions().canOpenInInspector);

  const hasSecondary = () =>
    (isImage() && actions().canOpenInInspector) ||
    actions().canOpenExternal ||
    Boolean(actions().copyMeta);

  const itemClass =
    "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] focus:bg-[var(--bg-hover)] focus:outline-none cursor-pointer transition-colors";

  return (
    <div
      ref={root}
      role="menu"
      tabIndex={-1}
      aria-label={t("timeline.attachmentContextMenu")}
      class="fixed z-[140] min-w-[188px] max-w-[min(280px,calc(100vw-12px))] max-h-[calc(100vh-12px)] overflow-y-auto rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-1 text-[var(--text-primary)] shadow-xl outline-none select-none"
      style={{ left: `${position().left}px`, top: `${position().top}px` }}
      onKeyDown={onKeyDown}
      onContextMenu={(event) => event.preventDefault()}
    >
      <Show when={actions().canCopyImage}>
        <button type="button" role="menuitem" class={itemClass} onClick={handleCopyImage}>
          <Copy size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">{t("timeline.copyImage")}</span>
        </button>
      </Show>

      <Show when={!isImage() && actions().canOpenInInspector}>
        <button type="button" role="menuitem" class={itemClass} onClick={handleOpenInInspector}>
          <FileText size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">
            {actions().openInInspectorLabel || t("timeline.openInInspector")}
          </span>
        </button>
      </Show>

      <Show when={actions().canDownload}>
        <button type="button" role="menuitem" class={itemClass} onClick={handleDownload}>
          <Download size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">{downloadLabelForKind(props.menu.target.kind)}</span>
        </button>
      </Show>

      <Show when={actions().canPreview}>
        <button type="button" role="menuitem" class={itemClass} onClick={handlePreview}>
          <ZoomIn size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">{t("timeline.previewFullSize")}</span>
        </button>
      </Show>

      <Show when={hasPrimary() && hasSecondary()}>
        <div role="separator" class="my-1 border-t border-[var(--border-subtle)]" />
      </Show>

      <Show when={isImage() && actions().canOpenInInspector}>
        <button type="button" role="menuitem" class={itemClass} onClick={handleOpenInInspector}>
          <FileText size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">
            {actions().openInInspectorLabel || t("timeline.openInInspector")}
          </span>
        </button>
      </Show>

      <Show when={actions().canOpenExternal}>
        <button
          type="button"
          role="menuitem"
          class={itemClass}
          onClick={() => handleOpenExternal("open")}
        >
          <ExternalLink size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">{t("inspector.files.openInDefaultApp")}</span>
        </button>
        <button
          type="button"
          role="menuitem"
          class={itemClass}
          onClick={() => handleOpenExternal("reveal")}
        >
          <FolderOpen size={13} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate">{t("inspector.files.revealInFinder")}</span>
        </button>
      </Show>

      <Show when={actions().copyMeta}>
        {(meta) => (
          <button type="button" role="menuitem" class={itemClass} onClick={handleCopyMeta}>
            <Show
              when={meta().kind === "link"}
              fallback={<Copy size={13} class="shrink-0 text-[var(--text-muted)]" />}
            >
              <Link2 size={13} class="shrink-0 text-[var(--text-muted)]" />
            </Show>
            <span class="truncate">{copyMetaLabel(meta().kind)}</span>
          </button>
        )}
      </Show>
    </div>
  );
}

export default function AttachmentContextMenu() {
  return (
    <>
      <Show when={attachmentContextMenu()}>
        {(menu) => (
          <Portal>
            <AttachmentContextMenuPopup menu={menu()} />
          </Portal>
        )}
      </Show>

      <Show when={attachmentActionFeedback()}>
        {(fb) => (
          <Portal>
            <div
              role="status"
              aria-live="polite"
              class={`pointer-events-none fixed bottom-6 left-1/2 z-[150] -translate-x-1/2 inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-[12px] font-medium shadow-lg backdrop-blur-md ${
                fb().tone === "error"
                  ? "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
                  : "border-[var(--border-subtle)] bg-[var(--bg-panel)]/95 text-[var(--text-primary)]"
              }`}
            >
              <Show
                when={fb().tone === "error"}
                fallback={<Check size={13} class="shrink-0 text-[var(--ok-text)]" />}
              >
                <AlertCircle size={13} class="shrink-0" />
              </Show>
              <span>{fb().text}</span>
            </div>
          </Portal>
        )}
      </Show>
    </>
  );
}
