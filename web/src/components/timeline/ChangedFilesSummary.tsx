import { createSignal, createUniqueId, For, Show } from "solid-js";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { t } from "../../i18n";
import { openDiffInInspector } from "../../store/workspace";
import { DiffChanges, FileIcon, Icon } from "../icons";
import { getFilename } from "./tool-card-helpers";

export default function ChangedFilesSummary(props: { files: SnapshotFileDiff[] }) {
  const [expanded, setExpanded] = createSignal(false);
  const listID = createUniqueId();

  return (
    <Show when={props.files.length > 0}>
      <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]">
        <button
          type="button"
          aria-expanded={expanded()}
          aria-controls={listID}
          onClick={() => setExpanded((value) => !value)}
          class="flex w-full items-center gap-2 px-3 py-2 text-left text-[12px] text-[var(--text-secondary)] transition-colors cursor-pointer hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--accent-primary)]"
        >
          <Icon name={expanded() ? "chevron-down" : "chevron-right"} size="small" />
          <span class="min-w-0 flex-1">{t("timeline.filesChangedCount", { count: props.files.length })}</span>
          <DiffChanges class="shrink-0" changes={props.files} showZero />
        </button>
        <div
          id={listID}
          hidden={!expanded()}
          class="border-t border-[var(--border-subtle)] p-2.5"
        >
          <Show when={expanded()}>
            <div class="flex items-center gap-1.5 flex-wrap">
              <For each={props.files}>
                {(diff) => (
                  <button
                    type="button"
                    onClick={() => openDiffInInspector(diff)}
                    title={diff.file}
                    class="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 font-mono text-[11.5px] text-[var(--text-secondary)] transition-colors cursor-pointer hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                  >
                    <FileIcon
                      node={{ path: diff.file, type: "file" }}
                      class="w-3.5 h-3.5 shrink-0"
                    />
                    <span class="truncate max-w-[220px]">{getFilename(diff.file)}</span>
                    <DiffChanges changes={diff} />
                  </button>
                )}
              </For>
            </div>
          </Show>
        </div>
      </div>
    </Show>
  );
}
