// web/src/__tests__/auto-session-affinity.test.ts
import {
  clearAutoPoolCooldowns,
  emptyModelStats,
  emptyStats,
  filterNoCacheCandidates,
  markAutoCandidateCooldown,
  purgeSessionsFromStoreState,
  resetAutoPoolToDefault,
  resolveAutoModelTarget,
  saveAutoPool,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

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

// Model-level telemetry for candidate "m" of each provider: [turns, cacheHitTurns].
function seedCacheTelemetry(byProvider: Record<string, [number, number]>) {
  const next: Record<string, ReturnType<typeof emptyStats>> = {};
  for (const [providerID, [turns, hits]] of Object.entries(byProvider)) {
    const stats = emptyStats(providerID);
    stats.models = { m: { ...emptyModelStats("m"), totalTurns: turns, successTurns: turns, cacheHitTurns: hits } };
    next[providerID] = stats;
  }
  setWorkspace("providerTelemetry", next);
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

  it("clears the pinned candidate when a session is purged", async () => {
    await seedTwoCandidateFreePool();

    const first = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_purge" });
    purgeSessionsFromStoreState(workspace, ["ses_aff_purge"]);
    const afterPurge = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_purge" });
    assertTrue(Boolean(first && afterPurge));
    assertTrue(first!.providerID !== afterPurge!.providerID);

    purgeSessionsFromStoreState(workspace, ["ses_aff_purge"]);
    await resetAutoPoolToDefault("free");
  });

  it("moves a pinned session off a channel that never reports prompt-cache hits", async () => {
    await seedTwoCandidateFreePool();
    setWorkspace("providerTelemetry", {});

    const a = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_nc_a" });
    const b = resolveAutoModelTarget("free", { advance: true, sessionID: "ses_aff_nc_b" });
    assertTrue(Boolean(a && b));
    assertTrue(a!.providerID !== b!.providerID);

    // alpha ran 30 turns without a single cache hit; beta hits the cache.
    seedCacheTelemetry({ alpha: [30, 0], beta: [30, 27] });
    for (const sid of ["ses_aff_nc_a", "ses_aff_nc_b", "ses_aff_nc_new"]) {
      assertDeepEqual(resolveAutoModelTarget("free", { advance: true, sessionID: sid }), {
        providerID: "beta",
        modelID: "m",
      });
    }

    setWorkspace("providerTelemetry", {});
    purgeSessionsFromStoreState(workspace, ["ses_aff_nc_a", "ses_aff_nc_b", "ses_aff_nc_new"]);
    await resetAutoPoolToDefault("free");
  });

  it("keeps unmeasured channels and never empties the pool", () => {
    const cands = [
      { providerID: "alpha", modelID: "m" },
      { providerID: "beta", modelID: "m" },
    ];
    // Too few turns to judge: both stay.
    seedCacheTelemetry({ alpha: [5, 0], beta: [30, 27] });
    assertEqual(filterNoCacheCandidates(cands).length, 2);
    // No model-level record: unknown, not "never".
    setWorkspace("providerTelemetry", {});
    assertEqual(filterNoCacheCandidates(cands).length, 2);
    // Every channel lacks cache: keep them all rather than strand the pool.
    seedCacheTelemetry({ alpha: [30, 0], beta: [40, 0] });
    assertEqual(filterNoCacheCandidates(cands).length, 2);
    setWorkspace("providerTelemetry", {});
  });
});
