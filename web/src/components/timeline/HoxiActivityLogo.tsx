import { observeSpinner } from "../icons/Spinner";
import "./hoxi-activity-logo.css";

export default function HoxiActivityLogo(props: { active?: boolean }) {
  // Each moving part is its own outer <svg> or <span>, so WebKit can run the
  // motion on the compositor; moving shapes inside one SVG repaints every frame.
  return (
    <span
      ref={observeSpinner}
      data-component="hoxi-activity-logo"
      data-active={props.active ?? true}
      aria-hidden="true"
    >
      <svg data-slot="orbit" viewBox="0 0 24 24" fill="none">
        <circle cx="12" cy="12" r="11.5" stroke="currentColor" stroke-width="1" opacity="0.1" />
        <circle
          data-slot="arc"
          cx="12"
          cy="12"
          r="11.5"
          pathLength="100"
          stroke="currentColor"
          stroke-width="1"
          stroke-linecap="round"
          stroke-dasharray="23 77"
        />
      </svg>
      {/* Preserve the favicon geometry inside a compact, tile-free face. */}
      <span data-slot="face">
        <svg viewBox="12 11 40 40" fill="none" overflow="visible">
          <g transform="translate(3.2, 4.8) scale(0.9)" fill="currentColor">
            <path d="M32 8L52 21H44L32 13L20 21H12L32 8Z" />
            <rect x="37.5" y="25" width="6.5" height="12" rx="3.25" />
            <path
              d="M19 42.5C23 50 41 50 45 42.5"
              stroke="currentColor"
              stroke-width="5.5"
              stroke-linecap="round"
              fill="none"
            />
          </g>
        </svg>
        <svg data-slot="eye" data-wink viewBox="12 11 40 40" fill="none" overflow="visible">
          <g transform="translate(3.2, 4.8) scale(0.9)" fill="currentColor">
            <rect x="20" y="25" width="6.5" height="12" rx="3.25" />
          </g>
        </svg>
      </span>
    </span>
  );
}
