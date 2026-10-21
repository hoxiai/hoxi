// web/src/__tests__/subagent-config.test.ts
import { describe, it, assertDeepEqual, assertEqual } from "./test-harness";
import {
  buildDelegationTaskValue,
  effectiveAllowedSubagents,
  readDelegationPolicy,
  readGlobalTaskPolicy,
  readSubagents,
  legacySubagentModel,
} from "../store/subagent-config";

const config = {
  permission: { task: "ask" },
  subagent_model: "legacy/model",
  agent: {
    general: { mode: "subagent", model: "p/general", steps: 5 },
    explore: { model: "p/explore" },
    scout: { mode: "subagent", disable: true },
    reviewer: {
      mode: "subagent",
      description: "reviews code",
      permission: { edit: "deny" },
      hidden: true,
    },
    build: { mode: "primary", permission: { task: { "*": "deny", reviewer: "allow" } } },
    plan: { mode: "primary", permission: { task: { "*": "deny" } } },
    compaction: { mode: "primary" },
  },
};

describe("subagent config", () => {
  it("lists builtin and custom subagents without primary or hidden system agents", () => {
    const ids = readSubagents(config).map((entry) => entry.id);
    assertDeepEqual(ids, ["general", "explore", "scout", "reviewer"]);
  });

  it("maps agent fields onto the roster entry", () => {
    const reviewer = readSubagents(config).find((entry) => entry.id === "reviewer");
    assertEqual(reviewer?.readOnly, true, "edit: deny is read-only");
    assertEqual(reviewer?.hidden, true);
    assertEqual(reviewer?.mode, "subagent");
    assertEqual(reviewer?.steps, 0, "unset steps means unlimited");
    const general = readSubagents(config).find((entry) => entry.id === "general");
    assertEqual(general?.steps, 5);
    assertEqual(general?.model, "p/general");
    const scout = readSubagents(config).find((entry) => entry.id === "scout");
    assertEqual(scout?.disabled, true);
  });

  it("reads the global task policy and the legacy subagent_model", () => {
    assertEqual(readGlobalTaskPolicy(config), "ask");
    assertEqual(readGlobalTaskPolicy({}), "inherit");
    assertEqual(legacySubagentModel(config), "legacy/model");
    assertEqual(legacySubagentModel({}), "");
  });

  it("distinguishes inherit, allow-all and allowlist delegation", () => {
    assertDeepEqual(readDelegationPolicy("build", config), {
      kind: "allowlist",
      allow: ["reviewer"],
    });
    assertDeepEqual(readDelegationPolicy("plan", config), { kind: "all", action: "deny" });
    assertDeepEqual(readDelegationPolicy("debug", config), { kind: "inherit" });
  });

  it("resolves which subagents a primary agent may invoke", () => {
    const ids = ["general", "explore", "scout", "reviewer"];
    assertDeepEqual(effectiveAllowedSubagents("build", ids, config), ["reviewer"]);
    assertDeepEqual(effectiveAllowedSubagents("plan", ids, config), []);
    assertEqual(effectiveAllowedSubagents("debug", ids, config), null, "inherit allows all");
    const deniedGlobal = { ...config, permission: { task: "deny" } };
    assertDeepEqual(effectiveAllowedSubagents("debug", ids, deniedGlobal), []);
    assertDeepEqual(effectiveAllowedSubagents("build", ids, deniedGlobal), ["reviewer"]);
  });

  it("builds task values that drop stale allowlist grants", () => {
    assertEqual(buildDelegationTaskValue({ "*": "deny", old: "allow" }, { kind: "inherit" }), null);
    assertEqual(buildDelegationTaskValue(undefined, { kind: "all", action: "ask" }), "ask");
    assertDeepEqual(
      buildDelegationTaskValue({ "*": "deny", old: "allow", reviewer: "allow" }, {
        kind: "allowlist",
        allow: ["reviewer"],
      }),
      { "*": "deny", reviewer: "allow", old: null }
    );
  });
});
