import { GitPullRequest } from "lucide-solid";
import type { ForgePullRequest } from "../../services/forge";
import { openExternalURL } from "../../store/workspace";
import { t } from "../../i18n";
import {
  PULL_REQUEST_TONE_CLASS,
  pullRequestTooltip,
  pullRequestTone,
} from "../inspector/pull-request-badge-helpers";

export default function SessionPullRequestPill(props: { pr: ForgePullRequest }) {
  return (
    <button
      type="button"
      onClick={(event) => {
        event.stopPropagation();
        openExternalURL(props.pr.url);
      }}
      aria-label={t("forge.open", { number: props.pr.number })}
      title={pullRequestTooltip(props.pr)}
      class={`hoxi-press shrink-0 inline-flex items-center gap-0.5 rounded border px-1 py-0.5 text-[10px] font-mono font-medium hover:opacity-90 cursor-pointer ${PULL_REQUEST_TONE_CLASS[pullRequestTone(props.pr)]}`}
    >
      <GitPullRequest size={9} />
      <span>#{props.pr.number}</span>
    </button>
  );
}
