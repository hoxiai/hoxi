import {
  completeSurfaceOnboarding,
  dismissSurfaceOnboarding,
  setSurfaceMode,
  surfaceMode,
  surfaceOnboardingOpen,
  waitForSurfaceOnboarding,
} from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

describe("first-launch surface onboarding", () => {
  it("holds startup until a surface is chosen, then applies it", async () => {
    const previous = surfaceMode();
    try {
      setSurfaceMode("code");
      let resolved: string | null = null;
      const pending = waitForSurfaceOnboarding().then((mode) => {
        resolved = mode;
        return mode;
      });
      assertEqual(surfaceOnboardingOpen(), true, "chooser opens");
      await Promise.resolve();
      assertEqual(resolved, null, "startup stays on hold before a choice");

      completeSurfaceOnboarding("work");
      assertEqual(await pending, "work");
      assertEqual(surfaceMode(), "work", "chosen surface applied");
      assertEqual(surfaceOnboardingOpen(), true, "chooser stays mounted for its exit animation");

      dismissSurfaceOnboarding();
      assertEqual(surfaceOnboardingOpen(), false, "chooser closes after dismissal");
    } finally {
      dismissSurfaceOnboarding();
      setSurfaceMode(previous);
    }
  });

  it("releases every waiter exactly once", async () => {
    const previous = surfaceMode();
    try {
      const first = waitForSurfaceOnboarding();
      const second = waitForSurfaceOnboarding();
      completeSurfaceOnboarding("code");
      assertEqual(await first, "code");
      assertEqual(await second, "code");

      let late = false;
      void waitForSurfaceOnboarding().then(() => {
        late = true;
      });
      completeSurfaceOnboarding("work");
      await Promise.resolve();
      assertEqual(late, true, "a later wait is served by the next choice");
    } finally {
      dismissSurfaceOnboarding();
      setSurfaceMode(previous);
    }
  });
});
