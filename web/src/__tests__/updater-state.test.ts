import {
  UPDATE_CHECK_MIN_GAP_MS,
  shouldAutoCheckUpdate,
  shouldNotifyUpdate,
} from "../store/updater-state";
import { assertEqual, describe, it } from "./test-harness";

describe("desktop update re-check policy", () => {
  it("checks on first run and throttles later automatic checks", () => {
    assertEqual(shouldAutoCheckUpdate(1_000, 0, false), true);
    assertEqual(shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS, 0, false), true);
    assertEqual(shouldAutoCheckUpdate(1_000, 1_000, false), false);
    assertEqual(
      shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS + 1_000, 1_000, false),
      true
    );
  });

  it("never stacks checks while one is already in flight", () => {
    assertEqual(shouldAutoCheckUpdate(1_000, 0, true), false);
    assertEqual(shouldAutoCheckUpdate(UPDATE_CHECK_MIN_GAP_MS * 10, 0, true), false);
  });
});

describe("desktop update notification policy", () => {
  it("notifies once per version only while the window is unattended", () => {
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", false, true), true);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "0.3.0", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "0.3.1", "0.3.0", false, true), true);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", true, true), false);
  });

  it("stays silent without an update, without a version or when notifications are off", () => {
    assertEqual(shouldNotifyUpdate(false, "0.3.0", "", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "   ", "", false, true), false);
    assertEqual(shouldNotifyUpdate(true, "0.3.0", "", false, false), false);
  });
});
