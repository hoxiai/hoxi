import {
  canFitRightPane,
  CENTER_PANE_MIN,
  clampRightPaneWidth,
  computeRightPaneCeiling,
  DEFAULT_LEFT_WIDTH,
  DEFAULT_RIGHT_WIDTH,
  PANE_GUTTERS,
  RIGHT_PANE_MAX,
  RIGHT_PANE_MIN,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("three-pane right inspector resize bounds", () => {
  it("allows wide free-dragging past 640px on standard and large displays", () => {
    const defaultCeiling = computeRightPaneCeiling(1360, DEFAULT_LEFT_WIDTH);
    assertTrue(
      defaultCeiling > 640,
      `1360px window ceiling (${defaultCeiling}px) should exceed legacy 640px cap`
    );
    assertEqual(defaultCeiling, 1360 - PANE_GUTTERS - DEFAULT_LEFT_WIDTH - CENTER_PANE_MIN);

    const laptopNoNavCeiling = computeRightPaneCeiling(1280, 0);
    assertEqual(laptopNoNavCeiling, 1280 - PANE_GUTTERS - CENTER_PANE_MIN);
    assertEqual(clampRightPaneWidth(800, 1280, 0), 800);

    const externalCeiling = computeRightPaneCeiling(2560, DEFAULT_LEFT_WIDTH);
    assertEqual(clampRightPaneWidth(1200, 2560, DEFAULT_LEFT_WIDTH), 1200);
    assertEqual(clampRightPaneWidth(9999, 5000, 0), RIGHT_PANE_MAX);
  });

  it("allows narrowing down to RIGHT_PANE_MIN and clamps gracefully on narrow viewports", () => {
    assertEqual(RIGHT_PANE_MIN, 260);
    assertEqual(clampRightPaneWidth(180, 1360, DEFAULT_LEFT_WIDTH), RIGHT_PANE_MIN);
    assertEqual(clampRightPaneWidth(DEFAULT_RIGHT_WIDTH, 1280, DEFAULT_LEFT_WIDTH), DEFAULT_RIGHT_WIDTH);
    assertEqual(canFitRightPane(1280, DEFAULT_LEFT_WIDTH), true);
    assertEqual(canFitRightPane(900, 0), true);
    assertEqual(canFitRightPane(880, DEFAULT_LEFT_WIDTH), false);
  });
});
