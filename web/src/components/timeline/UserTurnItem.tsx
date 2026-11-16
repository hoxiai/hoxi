// web/src/components/timeline/UserTurnItem.tsx
import { createSignal, Index, onCleanup, Show } from "solid-js";
import type { MessagePart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import {
  canForkSession,
  canRevertSession,
  forkFromMessage,
  openAttachmentInInspector,
  openImagePreview,
  resolveTurnSkillStatus,
  revertToHere,
  workspace,
} from "../../store/workspace";
import { FileIcon, Icon } from "../icons";
import {
  ActionIconButton,
  formatMessageTime,
  isAudioFilePart,
  isVideoFilePart,
  MediaAttachmentPlayer,
  UserTextPartView,
} from "./message-stream-parts";
import { t } from "../../i18n";

export function UserTurnItem(props: {
  turn: MessageTurn;
  index: number;
  globalIndex: number;
  allTurns: MessageTurn[];
  enterClass: string;
  isSessionBusy: boolean;
  isTaskActive: boolean;
  isHighlighted: boolean;
}) {
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer !== undefined) clearTimeout(copyTimer);
  });

  const onCopyUserMessage = async () => {
    const text = props.turn.parts
      .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
      .map((p) => p.text)
      .join("\n")
      .trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      if (copyTimer !== undefined) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => {
        copyTimer = undefined;
        setCopied(false);
      }, 1600);
    } catch {
      /* ignore */
    }
  };

  const onForkFromMessage = async () => {
    const id = workspace.activeSessionID;
    if (!id) return;
    try {
      await forkFromMessage(id, props.turn.info.id);
    } catch (err) {
      console.error("fork failed:", err);
    }
  };

  const onRevertToMessage = async () => {
    const id = workspace.activeSessionID;
    if (!id) return;
    try {
      await revertToHere(id, props.turn.info.id);
    } catch (err) {
      console.error("revert failed:", err);
    }
  };

  return (
    <div
      data-turn-id={props.turn.info.id}
      class={`${props.enterClass}group/turn flex flex-col items-end rounded-xl transition-colors ${
        props.index === 0 ? "mt-0" : "mt-6"
      } ${
        props.isHighlighted
          ? "ring-2 ring-[var(--accent-primary)] p-1.5"
          : ""
      }`}
    >
      <div class="max-w-[88%] rounded-2xl border border-[var(--accent-border)]/75 bg-[var(--accent-surface)] px-3.5 py-2.5 space-y-2">
        <Show
          when={resolveTurnSkillStatus(
            props.allTurns,
            props.globalIndex,
            props.isSessionBusy
          )}
        >
          {(skillInfo) => {
            const statusLabel = () => {
              const st = skillInfo().status;
              const name = skillInfo().skill.name;
              switch (st) {
                case "requested":
                  return `${name} · ${t("skills.statusRequested")}`;
                case "loading":
                  return `${name} · ${t("skills.statusLoading")}`;
                case "loaded":
                  return `${name} · ${t("skills.statusLoaded")}`;
                case "error":
                  return `${name} · ${t("skills.statusError")}`;
                case "unconfirmed":
                  return `${name} · ${t("skills.statusUnconfirmed")}`;
              }
            };
            const statusClass = () => {
              switch (skillInfo().status) {
                case "loaded":
                  return "border-emerald-500/30 bg-emerald-500/10 text-emerald-500";
                case "error":
                  return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
                case "unconfirmed":
                  return "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-muted)]";
                default:
                  return "border-[var(--accent-border)] bg-[var(--bg-panel)]/85 text-[var(--accent-text)]";
              }
            };
            return (
              <div class="flex items-center">
                <span
                  class={`inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[11px] font-medium ${statusClass()}`}
                >
                  <Icon name="brain" size="small" class="shrink-0" />
                  <span>{statusLabel()}</span>
                </span>
              </div>
            );
          }}
        </Show>
        <Index each={props.turn.parts}>
          {(part) => (
            <>
              <Show when={part().type === "file"}>
                {(() => {
                  const fp = part() as Extract<MessagePart, { type: "file" }>;
                  const isImg =
                    (fp.mime || "").toLowerCase().startsWith("image/") ||
                    /^data:image\//i.test(fp.url || "") ||
                    /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i.test(
                      fp.filename || ""
                    );
                  const extLabel =
                    (fp.mime || "").toLowerCase() === "application/pdf" ||
                    /\.pdf$/i.test(fp.filename || "")
                      ? "PDF"
                      : ((fp.filename || "").split(".").pop() || "FILE")
                          .toUpperCase()
                          .slice(0, 4);
                  const isSheet =
                    /\.(xlsx|xls|ods|csv|tsv)$/i.test(fp.filename || "") ||
                    (fp.mime || "").includes("spreadsheet") ||
                    (fp.mime || "").includes("excel") ||
                    (fp.mime || "").includes("csv");
                  const isMedia = !isImg && (isAudioFilePart(fp) || isVideoFilePart(fp));
                  const badgeColor = isSheet
                    ? "text-emerald-500"
                    : extLabel === "PDF"
                      ? "text-rose-500"
                      : "text-[var(--accent-primary)]";
                  return (
                    <Show
                      when={isImg}
                      fallback={
                        <Show
                          when={isMedia}
                          fallback={
                            <a
                              data-part-id={fp.id}
                              href={fp.url}
                              target="_blank"
                              rel="noreferrer"
                              download={fp.filename || "attachment"}
                              onClick={(e) => {
                                if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                                if (openAttachmentInInspector(fp.url, fp.filename)) {
                                  e.preventDefault();
                                }
                              }}
                              class="inline-flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] hover:border-[var(--accent-border)] transition-colors max-w-[260px]"
                            >
                              <FileIcon
                                node={{
                                  path: fp.filename || "file.txt",
                                  type: "file",
                                }}
                                class="h-4 w-4 shrink-0"
                              />
                              <span class="truncate font-mono text-[12px]">
                                {fp.filename || "attachment"}
                              </span>
                              <span class={`ml-auto shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] font-semibold ${badgeColor}`}>
                                {extLabel}
                              </span>
                            </a>
                          }
                        >
                          <MediaAttachmentPlayer part={fp} compact />
                        </Show>
                      }
                    >
                      <button
                        type="button"
                        data-part-id={fp.id}
                        onClick={() =>
                          openImagePreview({
                            src: fp.url,
                            alt: fp.filename || "attachment",
                          })
                        }
                        title={t("timeline.previewImageTooltip")}
                        class="block max-w-[260px] overflow-hidden rounded-lg border border-[var(--border-subtle)] cursor-zoom-in transition-colors hover:border-[var(--accent-border)]"
                      >
                        <img
                          src={fp.url}
                          alt={fp.filename || "attachment"}
                          class="max-h-48 w-auto object-contain block"
                        />
                      </button>
                    </Show>
                  );
                })()}
              </Show>
              <Show when={part().type === "text"}>
                <UserTextPartView
                  part={part() as Extract<MessagePart, { type: "text" }>}
                  forceExpand={props.isHighlighted}
                />
              </Show>
            </>
          )}
        </Index>
      </div>

      {/* Kilo-style User Message Hover Actions & Metadata */}
      <div class="mt-1 flex items-center justify-end gap-1 px-1 text-[12px] text-[var(--text-faint)] opacity-0 group-hover/turn:opacity-100 transition-opacity">
        <Show when={formatMessageTime(props.turn.info.time?.created)}>
          <span class="mr-1 font-mono text-[12px]">{formatMessageTime(props.turn.info.time?.created)}</span>
        </Show>

        <ActionIconButton
          label={copied() ? t("common.copied") : t("common.copy")}
          onClick={() => void onCopyUserMessage()}
        >
          <Show
            when={copied()}
            fallback={<Icon name="copy" size="small" />}
          >
            <Icon name="check" size="small" class="text-[var(--ok-text)]" />
          </Show>
        </ActionIconButton>

        <Show when={props.isTaskActive}>
          <ActionIconButton
            disabled={!canForkSession(workspace.activeSessionID)}
            label={
              !canForkSession(workspace.activeSessionID)
                ? t("timeline.runnerFeatureUnsupported")
                : t("timeline.forkBtn")
            }
            onClick={() => void onForkFromMessage()}
          >
            <Icon name="fork" size="small" />
          </ActionIconButton>

          <ActionIconButton
            disabled={!canRevertSession(workspace.activeSessionID)}
            variant="warn"
            label={
              !canRevertSession(workspace.activeSessionID)
                ? props.isSessionBusy
                  ? t("timeline.revertWhileBusy")
                  : t("timeline.runnerFeatureUnsupported")
                : t("timeline.revertBtn")
            }
            onClick={() => void onRevertToMessage()}
          >
            <Icon name="arrow-undo-down" size="small" />
          </ActionIconButton>
        </Show>
      </div>
    </div>
  );
}
