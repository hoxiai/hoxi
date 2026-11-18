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

function HoxiChanSvg(props: { mood: CompanionMood }) {
  return (
    <svg viewBox="0 0 120 120" class="h-full w-full overflow-visible" aria-hidden="true">
      {/* Twin-tail back silhouettes */}
      <path
        d="M26 48 C10 56, 8 86, 18 104 C24 98, 28 82, 32 66 Z"
        fill="var(--accent-primary)"
        fill-opacity="0.32"
      />
      <path
        d="M94 48 C110 56, 112 86, 102 104 C96 98, 92 82, 88 66 Z"
        fill="var(--accent-primary)"
        fill-opacity="0.32"
      />

      {/* Tech operator cape / hoodie shoulders */}
      <path
        d="M28 114 C30 92, 44 86, 60 86 C76 86, 90 92, 92 114 Z"
        fill="var(--bg-elevated)"
        stroke="var(--border-strong)"
        stroke-width="1.6"
      />
      <path
        d="M46 88 L60 103 L74 88"
        fill="none"
        stroke="var(--accent-primary)"
        stroke-width="2.4"
        stroke-linecap="round"
        stroke-linejoin="round"
      />

      {/* Main hair dome */}
      <path
        d="M25 56 C25 26, 42 15, 60 15 C78 15, 95 26, 95 56 C95 74, 88 86, 82 91 C82 76, 80 65, 76 58 L44 58 C40 65, 38 76, 38 91 C32 86, 25 74, 25 56 Z"
        fill="#261f33"
      />
      <path
        d="M29 50 C31 27, 45 18, 60 18 C75 18, 89 27, 91 50 C81 40, 70 36, 60 36 C50 36, 39 40, 29 50 Z"
        fill="var(--accent-primary)"
        fill-opacity="0.45"
      />

      {/* Face contour */}
      <path
        d="M36 53 C36 40, 46 34, 60 34 C74 34, 84 40, 84 53 C84 71, 74 83, 60 83 C46 83, 36 71, 36 53 Z"
        fill="#fff5f2"
        stroke="rgba(30, 41, 59, 0.22)"
        stroke-width="1.2"
      />

      {/* Soft anime blush */}
      <ellipse cx="44" cy="66" rx="5.2" ry="2.6" fill="#fb7185" fill-opacity="0.42" />
      <ellipse cx="76" cy="66" rx="5.2" ry="2.6" fill="#fb7185" fill-opacity="0.42" />

      {/* Front bangs */}
      <path
        d="M33 49 C37 33, 48 27, 60 27 C72 27, 83 33, 87 49 C80 43, 75 49, 71 53 C67 43, 62 47, 58 53 C53 44, 48 47, 44 53 C40 46, 36 45, 33 49 Z"
        fill="#261f33"
      />

      {/* Signature Hoxi 4-point star hairpin */}
      <path
        d="M81 30 L83.5 35.5 L89 38 L83.5 40.5 L81 46 L78.5 40.5 L73 38 L78.5 35.5 Z"
        fill="var(--accent-primary)"
      />

      {/* Dynamic Eyes & Mouth by Mood */}
      <Show when={props.mood === "gate-danger"}>
        <g stroke="#e11d48" stroke-width="2.4" stroke-linecap="round" fill="none">
          <path d="M43 56 L51 60 L43 64" />
          <path d="M77 56 L69 60 L77 64" />
        </g>
        <path
          d="M55 73 Q60 68 65 73"
          fill="none"
          stroke="#be123c"
          stroke-width="2"
          stroke-linecap="round"
        />
        {/* Stop Shield Prop */}
        <g transform="translate(82, 68)">
          <circle cx="14" cy="14" r="14" fill="#f43f5e" />
          <rect x="12.2" y="6.5" width="3.6" height="9.5" rx="1.8" fill="#ffffff" />
          <circle cx="14" cy="19.8" r="2" fill="#ffffff" />
        </g>
      </Show>

      <Show when={props.mood === "gate-approval"}>
        <g class="hoxi-companion-eye">
          <ellipse cx="48" cy="59" rx="4.2" ry="5" fill="#1e293b" />
          <ellipse cx="72" cy="59" rx="4.2" ry="5" fill="#1e293b" />
          <circle cx="49.5" cy="57" r="1.6" fill="#ffffff" />
          <circle cx="73.5" cy="57" r="1.6" fill="#ffffff" />
        </g>
        <circle cx="60" cy="72" r="2.6" fill="#d97706" />
        <g transform="translate(83, 69)">
          <circle cx="13" cy="13" r="13" fill="#f59e0b" />
          <text
            x="13"
            y="17.5"
            text-anchor="middle"
            fill="#ffffff"
            font-size="15"
            font-family="ui-monospace, monospace"
          >
            ?
          </text>
        </g>
      </Show>

      <Show when={props.mood === "failover"}>
        <g stroke="#1e293b" stroke-width="2.2" stroke-linecap="round" fill="none">
          <path d="M43 59 Q48 55 53 59" />
          <path d="M67 59 Q72 55 77 59" />
          <path d="M55 72 Q60 70 65 72" />
        </g>
        <g transform="translate(83, 69)">
          <circle cx="13" cy="13" r="13" fill="#f59e0b" />
          <path d="M14.5 6 L8.5 14 H13 L11.5 20 L17.5 12 H13 Z" fill="#ffffff" />
        </g>
      </Show>

      <Show when={props.mood === "coding"}>
        <g class="hoxi-companion-eye">
          <ellipse cx="48" cy="59" rx="4" ry="4.6" fill="#1e293b" />
          <ellipse cx="72" cy="59" rx="4" ry="4.6" fill="#1e293b" />
          <circle cx="49.5" cy="57.2" r="1.5" fill="#ffffff" />
          <circle cx="73.5" cy="57.2" r="1.5" fill="#ffffff" />
        </g>
        {/* Holo coding glasses */}
        <rect
          x="39"
          y="53"
          width="18"
          height="11"
          rx="4"
          fill="var(--accent-primary)"
          fill-opacity="0.16"
          stroke="var(--accent-primary)"
          stroke-width="1.6"
        />
        <rect
          x="63"
          y="53"
          width="18"
          height="11"
          rx="4"
          fill="var(--accent-primary)"
          fill-opacity="0.16"
          stroke="var(--accent-primary)"
          stroke-width="1.6"
        />
        <line x1="57" y1="58" x2="63" y2="58" stroke="var(--accent-primary)" stroke-width="1.6" />
        <path
          d="M55 71 Q57.5 74 60 71 Q62.5 74 65 71"
          fill="none"
          stroke="#475569"
          stroke-width="1.8"
          stroke-linecap="round"
        />
      </Show>

      <Show when={props.mood === "convoy"}>
        <g stroke="#1e293b" stroke-width="2.3" stroke-linecap="round" fill="none">
          <path d="M43 60 Q48 54 53 60" />
          <path d="M67 60 Q72 54 77 60" />
        </g>
        <path d="M54 70 Q60 78 66 70 Z" fill="#fb7185" />
        {/* Cheer Pennant */}
        <g transform="translate(85, 62)">
          <line x1="4" y1="4" x2="4" y2="30" stroke="var(--text-secondary)" stroke-width="2" stroke-linecap="round" />
          <path d="M4 5 L24 11 L4 17 Z" fill="var(--accent-primary)" />
        </g>
      </Show>

      <Show when={props.mood === "celebrate"}>
        <path d="M48 53 L49.6 57.4 L54 59 L49.6 60.6 L48 65 L46.4 60.6 L42 59 L46.4 57.4 Z" fill="var(--accent-primary)" />
        <path d="M72 53 L73.6 57.4 L78 59 L73.6 60.6 L72 65 L70.4 60.6 L66 59 L70.4 57.4 Z" fill="var(--accent-primary)" />
        <path d="M54 70 Q60 78 66 70 Z" fill="#fb7185" />
      </Show>

      <Show when={props.mood === "idle"}>
        <g class="hoxi-companion-eye">
          <ellipse cx="48" cy="59" rx="4.1" ry="4.8" fill="#1e293b" />
          <ellipse cx="72" cy="59" rx="4.1" ry="4.8" fill="#1e293b" />
          <circle cx="49.5" cy="57.2" r="1.6" fill="#ffffff" />
          <circle cx="73.5" cy="57.2" r="1.6" fill="#ffffff" />
        </g>
        <path
          d="M55 71 Q60 75 65 71"
          fill="none"
          stroke="#475569"
          stroke-width="1.9"
          stroke-linecap="round"
        />
      </Show>
    </svg>
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
            fallback={<HoxiChanSvg mood={props.mood} />}
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
