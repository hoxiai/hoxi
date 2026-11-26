// Unified 7x8 circular dot-matrix H brand mark geometry (~66% body duty cycle).
// Shared across SVG activity indicators and Canvas share cards so radii and pitch stay aligned.
export const HOXI_MATRIX_VIEWBOX = 20;
export const HOXI_MATRIX_STEP = 2.1;
export const HOXI_MATRIX_BODY_RADIUS = 0.69;
export const HOXI_MATRIX_SPARK_RADIUS = 0.73;

export const HOXI_MATRIX_BODY_DOTS: readonly [number, number][] = [
  [3.7, 2.65], [5.8, 2.65], [14.2, 2.65], [16.3, 2.65],
  [3.7, 4.75], [5.8, 4.75], [14.2, 4.75], [16.3, 4.75],
  [3.7, 6.85], [5.8, 6.85], [14.2, 6.85], [16.3, 6.85],
  [3.7, 8.95], [5.8, 8.95], [7.9, 8.95], [12.1, 8.95], [14.2, 8.95], [16.3, 8.95],
  [3.7, 11.05], [5.8, 11.05], [7.9, 11.05], [12.1, 11.05], [14.2, 11.05], [16.3, 11.05],
  [3.7, 13.15], [5.8, 13.15], [14.2, 13.15], [16.3, 13.15],
  [3.7, 15.25], [5.8, 15.25], [14.2, 15.25], [16.3, 15.25],
  [3.7, 17.35], [5.8, 17.35], [14.2, 17.35], [16.3, 17.35],
];

export const HOXI_MATRIX_SPARK_DOTS: readonly [number, number][] = [
  [10, 8.95],
  [10, 11.05],
];
