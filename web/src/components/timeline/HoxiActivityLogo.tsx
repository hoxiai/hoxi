import { observeSpinner } from "../icons/Spinner";
import "./hoxi-activity-logo.css";

export default function HoxiActivityLogo(props: { active?: boolean }) {
  return (
    <svg
      ref={observeSpinner}
      data-component="hoxi-activity-logo"
      data-active={props.active ?? true}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="11.5" stroke="currentColor" stroke-width="1" opacity="0.1" />
      <circle
        data-slot="orbit"
        cx="12"
        cy="12"
        r="11.5"
        pathLength="100"
        stroke="currentColor"
        stroke-width="1"
        stroke-linecap="round"
        stroke-dasharray="23 77"
      />
      {/* Preserve the favicon geometry inside a compact, tile-free face. */}
      <svg x="3" y="3" width="18" height="18" viewBox="12 11 40 40" overflow="visible">
        <g transform="translate(3.2, 4.8) scale(0.9)" fill="currentColor">
          <g data-slot="face">
            <path d="M32 8L52 21H44L32 13L20 21H12L32 8Z" />
            <rect data-slot="eye" data-wink x="20" y="25" width="6.5" height="12" rx="3.25" />
            <rect data-slot="eye" x="37.5" y="25" width="6.5" height="12" rx="3.25" />
            <path
              d="M19 42.5C23 50 41 50 45 42.5"
              stroke="currentColor"
              stroke-width="5.5"
              stroke-linecap="round"
              fill="none"
            />
          </g>
        </g>
      </svg>
    </svg>
  );
}
