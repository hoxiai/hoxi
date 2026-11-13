import "../transitions.css";
import { assertEqual, assertTrue } from "./test-harness";

export async function runBrowserTest(): Promise<string[]> {
  const host = document.createElement("div");
  host.style.cssText = "--motion-enter:20ms;--motion-move:20ms;--ease-spring:linear;--ease-enter:linear;width:600px";
  document.body.append(host);
  const classes = [
    "animate-in", "hoxi-view-enter", "hoxi-view-enter-a", "hoxi-view-enter-b",
    "hoxi-drill-enter", "hoxi-drill-enter-a", "hoxi-drill-enter-b", "hoxi-fade-enter",
    "hoxi-item-enter", "hoxi-item-pop", "hoxi-step-enter",
  ];
  const rows = classes.map((name) => {
    const row = document.createElement("div");
    row.className = name;
    row.style.cssText = "height:100px;animation-duration:20ms;animation-delay:40ms";
    row.textContent = name;
    host.append(row);
    return row;
  });
  try {
    const animations = rows.flatMap((row) => row.getAnimations());
    assertEqual(animations.length, classes.length, "each entrance must actually animate");
    for (const row of rows) {
      assertTrue(Number(getComputedStyle(row).opacity) < 1, `${row.className} flashes before its delayed entrance`);
    }
    await Promise.all(animations.map((animation) => animation.finished));
    await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
    const retained = rows.filter((row) => getComputedStyle(row).transform !== "none" || row.getAnimations().length > 0);
    assertEqual(retained.length, 0, `finished entrances retain animation/layer state: ${retained.map((row) => row.className).join(", ")}`);
    for (const row of rows) {
      assertEqual(getComputedStyle(row).opacity, "1");
      assertEqual(row.getBoundingClientRect().height, 100, "settling the entrance must preserve layout");
    }
    return ["11 delayed entrances keep their initial opacity, then release all finished animation/transform state without changing layout"];
  } finally { host.remove(); }
}
