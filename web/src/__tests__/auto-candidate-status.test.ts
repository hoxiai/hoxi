// web/src/__tests__/auto-candidate-status.test.ts
import {
  applyProviderBreakers,
  clearAutoPoolCooldowns,
  getCandidatePoolStatus,
  markAutoCandidateCooldown,
  reportAutoCandidateOutcome,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("auto-pool candidate status derivation", () => {
  it("reports a fresh candidate as ready with no cooldown", () => {
    applyProviderBreakers({});
    const st = getCandidatePoolStatus("alpha", "m");
    assertEqual(st.state, "ready");
    assertEqual(st.remainMs, 0);
  });

  it("reports an open breaker with its failure class and remaining cooldown", () => {
    applyProviderBreakers({});
    reportAutoCandidateOutcome({
      providerID: "alpha",
      modelID: "m",
      ok: false,
      statusCode: 429,
      class: "rate_limit",
    });
    const st = getCandidatePoolStatus("alpha", "m");
    assertEqual(st.state, "breaker");
    assertTrue(st.remainMs > 0);
    assertEqual(st.failClass, "rate_limit");
  });

  it("reports a local session cooldown with its reason", () => {
    applyProviderBreakers({});
    markAutoCandidateCooldown("beta", "m", "429 rate limit", 429);
    const st = getCandidatePoolStatus("beta", "m");
    assertEqual(st.state, "cooldown");
    assertTrue(st.remainMs > 0);
    assertEqual(st.reason, "429 rate limit");
    clearAutoPoolCooldowns();
  });

  it("treats an open breaker as authoritative over a local cooldown", () => {
    applyProviderBreakers({});
    markAutoCandidateCooldown("gamma", "m", "quota exhausted");
    reportAutoCandidateOutcome({ providerID: "gamma", modelID: "m", ok: false, class: "quota" });
    assertEqual(getCandidatePoolStatus("gamma", "m").state, "breaker");
    clearAutoPoolCooldowns();
  });
});
