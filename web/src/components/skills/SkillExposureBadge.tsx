// web/src/components/skills/SkillExposureBadge.tsx
import { Show } from "solid-js";
import { t } from "../../i18n";
import type { SkillDescriptor } from "../../services/api";
import { skillExposureReason } from "../../store/workspace";

/** Marks skills the model does not see in full: on-demand or unavailable. */
export default function SkillExposureBadge(props: { item: SkillDescriptor }) {
  const label = () =>
    props.item.exposure === "unavailable"
      ? t("skills.exposureUnavailable")
      : props.item.exposure === "on-demand"
        ? t("skills.exposureOnDemand")
        : "";
  return (
    <Show when={label()}>
      <span
        title={skillExposureReason(props.item)}
        class={`rounded-md px-1.5 py-0.5 text-[11px] font-medium ${
          props.item.exposure === "unavailable"
            ? "bg-rose-500/10 text-rose-500"
            : "bg-[var(--bg-hover)] text-[var(--text-muted)]"
        }`}
      >
        {label()}
      </span>
    </Show>
  );
}
