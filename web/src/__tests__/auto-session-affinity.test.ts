// web/src/__tests__/auto-session-affinity.test.ts
import {
  clearAutoPoolCooldowns,
  markAutoCandidateCooldown,
  resetAutoPoolToDefault,
  resolveAutoModelTarget,
  saveAutoPool,
  setWorkspace,
} from "../store/workspace";
import { assertDeepEqual, assertTrue, describe, it } from "./test-harness";

async function seedTwoCandidateFreePool() {
  setWorkspace("providers", [
    { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
  ]);
  setWorkspace("connectedProviders", ["alpha", "beta"]);
  await saveAutoPool("free", {
    id: "free",
    preset: true,
    strategy: "round-robin",
    customized: true,
    candidates: [
      { providerID: "alpha", modelID: "m" },
      { providerID: "beta", modelID: "m" },
    ],
  });
  clearAutoPoolCooldowns("free");
}

describe("auto pool session affinity", () => {
  it("keeps one session pinned to the same candidate across turns", async () => {
    await seedTwoCandidateFreePool();

    const first = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_a" });
    const second = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_a" });
    const third = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_a" });
    assertTrue(Boolean(first && second && third));
    assertDeepEqual(second, first);
    assertDeepEqual(third, first);

    await resetAutoPoolToDefault("free");
  });

  it("spreads distinct sessions across candidates but keeps each one pinned", async () => {
    await seedTwoCandidateFreePool();

    const a = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_x" });
    const b = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_y" });
    assertTrue(Boolean(a && b));
    assertTrue(a!.providerID !== b!.providerID);

    // The second session consumed a cursor slot; the first must still be pinned.
    assertDeepEqual(resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_x" }), a);

    await resetAutoPoolToDefault("free");
  });

  it("fails over when the pinned candidate is cooling down and then stays put", async () => {
    await seedTwoCandidateFreePool();

    const first = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_fail" });
    assertTrue(Boolean(first));
    markAutoCandidateCooldown(first!.providerID, first!.modelID, "429 Too Many Requests", 429);

    const next = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_fail" });
    assertTrue(Boolean(next));
    assertTrue(next!.providerID !== first!.providerID);
    assertDeepEqual(
      resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_fail" }),
      next
    );

    clearAutoPoolCooldowns("free");
    await resetAutoPoolToDefault("free");
  });

  it("still rotates when no session is attached (pool preview / backend callers)", async () => {
    await seedTwoCandidateFreePool();

    const first = resolveAutoModelTarget("free", { advance: true });
    const second = resolveAutoModelTarget("free", { advance: true });
    assertTrue(Boolean(first && second));
    assertTrue(first!.providerID !== second!.providerID);

    await resetAutoPoolToDefault("free");
  });
});
