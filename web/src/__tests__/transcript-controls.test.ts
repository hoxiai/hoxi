import { createRoot } from "solid-js";
import { createTranscriptControls } from "../components/timeline/transcript-controls";
import { assertEqual, describe, it } from "./test-harness";

describe("Transcript floating controls", () => {
  it("ignores automatic scrolling and reveals only after deliberate upward travel", () => {
    createRoot((dispose) => {
      const controls = createTranscriptControls(() => "session");
      controls.onScroll(300);
      controls.onScroll(200);
      assertEqual(controls.visible(), false);
      controls.markIntent();
      controls.onScroll(190);
      assertEqual(controls.visible(), false);
      controls.onScroll(170);
      assertEqual(controls.visible(), true);
      controls.onScroll(180);
      assertEqual(controls.visible(), false);
      controls.onScroll(140);
      assertEqual(controls.visible(), true);
      dispose();
    });
  });
});
