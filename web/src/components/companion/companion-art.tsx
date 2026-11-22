// web/src/components/companion/companion-art.tsx
import { Show } from "solid-js";
import { t } from "../../i18n";
import {
  formatCompactTokens,
  type CompanionMood,
  type CompanionPersona,
  type CompanionSnapshot,
} from "../../store/workspace";

export type MoodBadgeTone = "danger" | "warn" | "info" | "ok" | "accent" | "muted";

export function resolveCompanionPersonaName(persona: CompanionPersona): string {
  if (persona === "neko-bot") return t("companion.nameNekoBot");
  if (persona === "custom") return t("companion.nameCustom");
  return t("companion.nameHoxiChan");
}

export function resolveCompanionMoodBadge(snap: CompanionSnapshot): {
  label: string;
  tone: MoodBadgeTone;
} {
  switch (snap.mood) {
    case "gate-danger":
      return { label: t("companion.badgeGateDanger"), tone: "danger" };
    case "gate-approval":
      return {
        label: t("companion.badgeGateApproval", { count: snap.pendingCount }),
        tone: "warn",
      };
    case "failover":
      return { label: t("companion.badgeFailover"), tone: "warn" };
    case "convoy":
      return {
        label: t("companion.badgeConvoy", { count: snap.parallelCount }),
        tone: "accent",
      };
    case "coding":
      return { label: t("companion.badgeCoding"), tone: "info" };
    case "celebrate":
      return { label: t("companion.badgeCelebrate"), tone: "ok" };
    default:
      return { label: t("companion.badgeIdle"), tone: "muted" };
  }
}

export function resolveCompanionDialogue(
  snap: CompanionSnapshot,
  colorScheme: string
): string {
  switch (snap.mood) {
    case "gate-danger":
      return t("companion.lineGateDanger", { target: snap.gateTarget || "bash" });
    case "gate-approval":
      return t("companion.lineGateApproval", { count: Math.max(1, snap.pendingCount) });
    case "failover":
      return t("companion.lineFailover", { attempt: Math.max(1, snap.retryAttempt) });
    case "convoy":
      return t("companion.lineConvoy", { count: Math.max(2, snap.parallelCount) });
    case "coding":
      return t("companion.lineCoding");
    case "celebrate":
      return t("companion.lineCelebrate");
    default: {
      if (snap.isPoked) {
        if (snap.pokeSlot === 1) return t("companion.poke1");
        if (snap.pokeSlot === 2) return t("companion.poke2");
        if (snap.pokeSlot === 3) return t("companion.poke3");
        return t("companion.poke4");
      }
      if (colorScheme === "obsidian") {
        return t("companion.idleObsidian");
      }
      if (snap.timeSlot === "morning") return t("companion.idleMorning");
      if (snap.timeSlot === "afternoon") return t("companion.idleAfternoon");
      if (snap.timeSlot === "evening") return t("companion.idleEvening");
      return t("companion.idleLateNight");
    }
  }
}

export function formatCompanionTokensText(todayTokens: number): string {
  return t("companion.todayTokensLabel", { tokens: formatCompactTokens(todayTokens) });
}

export const HOXI_CHAN_CHIBI_URI = "/companion/hoxi-chan.png";

function HoxiChanChibi(props: { mood: CompanionMood }) {
  return (
    <div class="relative h-full w-full">
      <img
        src={HOXI_CHAN_CHIBI_URI}
        alt=""
        aria-hidden="true"
        draggable={false}
        class="h-full w-full object-contain drop-shadow-sm select-none"
      />
      <Show when={props.mood !== "idle"}>
        <svg
          viewBox="0 0 120 120"
          class="pointer-events-none absolute inset-0 h-full w-full overflow-visible"
          aria-hidden="true"
        >
          <Show when={props.mood === "gate-danger"}>
            <g transform="translate(86, 82)">
              <circle cx="15" cy="15" r="15" fill="#f43f5e" stroke="#ffffff" stroke-width="2" />
              <rect x="13.1" y="6.8" width="3.8" height="10" rx="1.9" fill="#ffffff" />
              <circle cx="15" cy="21.2" r="2.1" fill="#ffffff" />
            </g>
          </Show>

          <Show when={props.mood === "gate-approval"}>
            <g transform="translate(86, 82)">
              <circle cx="15" cy="15" r="15" fill="#f59e0b" stroke="#ffffff" stroke-width="2" />
              <text
                x="15"
                y="20.2"
                text-anchor="middle"
                fill="#ffffff"
                font-size="16.5"
                font-weight="700"
                font-family="ui-monospace, monospace"
              >
                ?
              </text>
            </g>
          </Show>

          <Show when={props.mood === "failover"}>
            <g transform="translate(86, 82)">
              <circle cx="15" cy="15" r="15" fill="#f59e0b" stroke="#ffffff" stroke-width="2" />
              <path d="M16.8 6.8 L10.2 15.8 H15.2 L13.4 23.2 L20 14.2 H15 Z" fill="#ffffff" />
            </g>
          </Show>

          <Show when={props.mood === "coding"}>
            <g transform="translate(80, 84)">
              <rect
                x="0"
                y="0"
                width="36"
                height="24"
                rx="8"
                fill="var(--bg-elevated)"
                stroke="var(--accent-primary)"
                stroke-width="2"
              />
              <text
                x="18"
                y="16"
                text-anchor="middle"
                fill="var(--accent-primary)"
                font-size="12"
                font-weight="700"
                font-family="ui-monospace, monospace"
              >
                {"</>"}
              </text>
            </g>
          </Show>

          <Show when={props.mood === "convoy"}>
            <g transform="translate(88, 10)">
              <line
                x1="4"
                y1="4"
                x2="4"
                y2="34"
                stroke="var(--text-secondary)"
                stroke-width="2.4"
                stroke-linecap="round"
              />
              <path d="M4 5 L28 12 L4 19 Z" fill="var(--accent-primary)" />
            </g>
          </Show>

          <Show when={props.mood === "celebrate"}>
            <path
              d="M18 14 L20.4 20.6 L27 23 L20.4 25.4 L18 32 L15.6 25.4 L9 23 L15.6 20.6 Z"
              fill="#fbbf24"
            />
            <path
              d="M103 12 L105.2 17.8 L111 20 L105.2 22.2 L103 28 L100.8 22.2 L95 20 L100.8 17.8 Z"
              fill="var(--accent-primary)"
            />
          </Show>
        </svg>
      </Show>
    </div>
  );
}

function NekoBotSvg(props: { mood: CompanionMood }) {
  const visorText = () => {
    switch (props.mood) {
      case "gate-danger":
        return "! >_< !";
      case "gate-approval":
        return "? o_o ?";
      case "failover":
        return "~_~ ⚡";
      case "convoy":
        return "⚑ ^ω^ ⚑";
      case "coding":
        return "⌐■_■";
      case "celebrate":
        return "★ ▽ ★";
      default:
        return "^ ω ^";
    }
  };

  return (
    <svg viewBox="0 0 120 120" class="h-full w-full overflow-visible" aria-hidden="true">
      {/* Cyber tail */}
      <path
        d="M86 96 C104 94, 108 76, 98 68"
        fill="none"
        stroke="var(--accent-primary)"
        stroke-width="5"
        stroke-linecap="round"
      />
      {/* Ears */}
      <path d="M26 52 L22 20 L48 36 Z" fill="#18181b" stroke="var(--border-strong)" stroke-width="1.5" />
      <path d="M94 52 L98 20 L72 36 Z" fill="#18181b" stroke="var(--border-strong)" stroke-width="1.5" />
      <path d="M29 46 L27 27 L43 37 Z" fill="var(--accent-primary)" fill-opacity="0.85" />
      <path d="M91 46 L93 27 L77 37 Z" fill="var(--accent-primary)" fill-opacity="0.85" />

      {/* Body */}
      <rect
        x="34"
        y="82"
        width="52"
        height="28"
        rx="14"
        fill="#18181b"
        stroke="var(--border-strong)"
        stroke-width="1.5"
      />
      {/* Bell collar */}
      <line x1="42" y1="84" x2="78" y2="84" stroke="var(--accent-primary)" stroke-width="3" stroke-linecap="round" />
      <circle cx="60" cy="87" r="4.5" fill="#fbbf24" />

      {/* Head shell */}
      <rect
        x="22"
        y="34"
        width="76"
        height="52"
        rx="24"
        fill="#18181b"
        stroke="var(--border-strong)"
        stroke-width="1.6"
      />
      {/* LED Visor */}
      <rect
        x="30"
        y="44"
        width="60"
        height="31"
        rx="13"
        fill="#09090b"
        stroke={props.mood === "gate-danger" ? "#f43f5e" : "var(--accent-primary)"}
        stroke-width="1.5"
      />
      <text
        x="60"
        y="63"
        text-anchor="middle"
        fill={props.mood === "gate-danger" ? "#fb7185" : "var(--accent-primary)"}
        font-size="12.5"
        font-family="ui-monospace, SFMono-Regular, Menlo, monospace"
      >
        {visorText()}
      </text>
    </svg>
  );
}

export function CompanionAvatar(props: {
  persona: CompanionPersona;
  mood: CompanionMood;
  customUrl?: string;
  size?: "stage" | "dock" | "mini";
}) {
  const dimClass = () => {
    if (props.size === "stage") return "h-24 w-24";
    if (props.size === "mini") return "h-9 w-9";
    return "h-14 w-14";
  };

  return (
    <div
      class={`relative shrink-0 select-none ${dimClass()} ${
        props.mood === "gate-danger"
          ? "hoxi-companion-alert"
          : "hoxi-companion-float"
      }`}
    >
      <Show
        when={props.persona === "custom" && Boolean(props.customUrl)}
        fallback={
          <Show
            when={props.persona === "neko-bot"}
            fallback={<HoxiChanChibi mood={props.mood} />}
          >
            <NekoBotSvg mood={props.mood} />
          </Show>
        }
      >
        <img
          src={props.customUrl}
          alt={t("companion.nameCustom")}
          class={`h-full w-full object-contain drop-shadow-md ${
            props.size === "mini" ? "rounded-xl" : "rounded-2xl"
          }`}
        />
        <Show when={props.mood === "gate-danger"}>
          <span class="absolute -right-1 -top-1 inline-flex h-5 w-5 items-center justify-center rounded-full bg-[var(--status-danger)] text-[11px] font-bold text-white shadow-sm">
            !
          </span>
        </Show>
      </Show>
    </div>
  );
}
