import { observeSpinner } from "./Spinner";

export function ArcSpinner(props: { active?: boolean }) {
  return (
    <svg
      ref={observeSpinner}
      data-component="arc-spinner"
      data-active={props.active ?? true}
      viewBox="0 0 14 14"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="7" cy="7" r="6.5" stroke="currentColor" stroke-width="1" opacity="0.1" />
      <circle
        data-slot="orbit"
        cx="7"
        cy="7"
        r="6.5"
        pathLength="100"
        stroke="currentColor"
        stroke-width="1"
        stroke-linecap="round"
        stroke-dasharray="23 77"
      />
    </svg>
  );
}
