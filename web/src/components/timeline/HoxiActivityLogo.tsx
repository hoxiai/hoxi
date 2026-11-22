import { observeSpinner } from "../icons/Spinner";
import "./hoxi-activity-logo.css";

// 7x8 fine dot-matrix H (36 body dots + 2 center spark dots) in a 20x20 viewBox.
const BODY_DOTS: readonly [number, number][] = [
  [3.7, 2.65], [5.8, 2.65], [14.2, 2.65], [16.3, 2.65],
  [3.7, 4.75], [5.8, 4.75], [14.2, 4.75], [16.3, 4.75],
  [3.7, 6.85], [5.8, 6.85], [14.2, 6.85], [16.3, 6.85],
  [3.7, 8.95], [5.8, 8.95], [7.9, 8.95], [12.1, 8.95], [14.2, 8.95], [16.3, 8.95],
  [3.7, 11.05], [5.8, 11.05], [7.9, 11.05], [12.1, 11.05], [14.2, 11.05], [16.3, 11.05],
  [3.7, 13.15], [5.8, 13.15], [14.2, 13.15], [16.3, 13.15],
  [3.7, 15.25], [5.8, 15.25], [14.2, 15.25], [16.3, 15.25],
  [3.7, 17.35], [5.8, 17.35], [14.2, 17.35], [16.3, 17.35],
];

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
      {/* Fine 7x8 dot-matrix H matching the unified Hoxi brand mark. */}
      <span data-slot="face">
        <svg viewBox="0 0 20 20" fill="currentColor" overflow="visible">
          {BODY_DOTS.map(([cx, cy]) => (
            <circle cx={cx} cy={cy} r="0.76" />
          ))}
        </svg>
        <svg data-slot="spark" viewBox="0 0 20 20" fill="currentColor" overflow="visible">
          <circle cx="10" cy="8.95" r="0.82" />
          <circle cx="10" cy="11.05" r="0.82" />
        </svg>
      </span>
    </span>
  );
}
