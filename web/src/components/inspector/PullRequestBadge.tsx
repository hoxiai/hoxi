import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { ExternalLink, GitPullRequest, GitPullRequestArrow } from "lucide-solid";
import {
  fetchPullRequest,
  type ForgePullRequest,
} from "../../services/forge";
import { openExternalURL } from "../../store/workspace";
import { t } from "../../i18n";

import {
  PULL_REQUEST_TONE_CLASS,
  pullRequestTooltip,
  pullRequestTone,
} from "./pull-request-badge-helpers";

export default function PullRequestBadge(props: {
  directory?: string;
  branch?: string;
  compareURL?: string;
  canCreate?: boolean;
  disabled?: boolean;
  compact?: boolean;
  onCreate?: () => void | Promise<void>;
}) {
  const [pr, setPr] = createSignal<ForgePullRequest | null>(null);
  const [loaded, setLoaded] = createSignal(false);
  let generation = 0;

  createEffect(() => {
    const directory = (props.directory ?? "").trim();
    const branch = (props.branch ?? "").trim();
    const current = ++generation;
    setPr(null);
    setLoaded(false);
    if (!directory) return;

    let disposed = false;
    const load = async (force: boolean) => {
      try {
        const response = await fetchPullRequest(directory, branch, force);
        if (disposed || current !== generation) return;
        setPr(response.available && response.pullRequest ? response.pullRequest : null);
      } catch {
        if (!disposed && current === generation) setPr(null);
      } finally {
        if (!disposed && current === generation) setLoaded(true);
      }
    };

    void load(false);
    const timer = window.setInterval(() => void load(true), 60_000);
    onCleanup(() => {
      disposed = true;
      window.clearInterval(timer);
    });
  });

  const activate = () => {
    const current = pr();
    if (current) {
      openExternalURL(current.url);
      return;
    }
    if (props.onCreate) {
      void props.onCreate();
      return;
    }
    if (props.compareURL) openExternalURL(props.compareURL);
  };

  const sizeClass = () => (props.compact ? "px-1.5 py-0.5" : "px-2 py-1");

  return (
    <Show
      when={pr()}
      fallback={
        <Show when={loaded() && props.canCreate && props.compareURL}>
          <button
            type="button"
            disabled={props.disabled}
            onClick={activate}
            title={t("inspector.diffs.createPrTooltip")}
            class={`hoxi-press shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer ${sizeClass()}`}
          >
            <GitPullRequestArrow size={10} />
            <span>{t("inspector.diffs.createPrBtn")}</span>
            <ExternalLink size={8} />
          </button>
        </Show>
      }
    >
      {(current) => (
        <button
          type="button"
          disabled={props.disabled}
          onClick={activate}
          aria-label={t("forge.open", { number: current().number })}
          title={pullRequestTooltip(current())}
          class={`hoxi-press shrink-0 inline-flex items-center gap-1 rounded-md border text-[11px] font-mono font-medium hover:opacity-90 disabled:opacity-40 cursor-pointer ${sizeClass()} ${PULL_REQUEST_TONE_CLASS[pullRequestTone(current())]}`}
        >
          <GitPullRequest size={10} />
          <span>#{current().number}</span>
        </button>
      )}
    </Show>
  );
}
