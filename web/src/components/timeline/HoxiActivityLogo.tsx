import { observeSpinner } from "../icons/Spinner";
import {
  HOXI_MATRIX_BODY_DOTS,
  HOXI_MATRIX_BODY_RADIUS,
  HOXI_MATRIX_SPARK_DOTS,
  HOXI_MATRIX_SPARK_RADIUS,
  HOXI_MATRIX_VIEWBOX,
} from "../icons/hoxi-brand-mark";
import "./hoxi-activity-logo.css";

const MATRIX_VIEWBOX = `0 0 ${HOXI_MATRIX_VIEWBOX} ${HOXI_MATRIX_VIEWBOX}`;

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
        <svg viewBox={MATRIX_VIEWBOX} fill="currentColor" overflow="visible">
          {HOXI_MATRIX_BODY_DOTS.map(([cx, cy]) => (
            <circle cx={cx} cy={cy} r={HOXI_MATRIX_BODY_RADIUS} />
          ))}
        </svg>
        <svg data-slot="spark" viewBox={MATRIX_VIEWBOX} fill="currentColor" overflow="visible">
          {HOXI_MATRIX_SPARK_DOTS.map(([cx, cy]) => (
            <circle cx={cx} cy={cy} r={HOXI_MATRIX_SPARK_RADIUS} />
          ))}
        </svg>
      </span>
    </span>
  );
}
