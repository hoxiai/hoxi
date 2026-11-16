import assert from "node:assert/strict";
import { test } from "node:test";
import BrowserPlugin from "./browser_plugin.mjs";
import { rewriteSkillCatalog } from "./skill_catalog.js";

// Mirrors OpenCode 1.19.3 SystemPrompt.skills output (verbose fmt).
function upstreamCatalog(skills) {
  return [
    "Skills provide specialized instructions and workflows for specific tasks.",
    "Use the skill tool to load a skill when a task matches its description.",
    "<available_skills>",
    ...skills.flatMap((s) => [
      "  <skill>",
      `    <name>${s}</name>`,
      `    <description>Long description of ${s} that should not stay resident.</description>`,
      `    <location>/home/u/skills/${s}/SKILL.md</location>`,
      "  </skill>",
    ]),
    "</available_skills>",
  ].join("\n");
}

const SKILLS = ["amazon-ops", "built-in-browser", "customize-opencode", "review-changes", "us-options-ops"];
const EXPOSURES = [
  { name: "amazon-ops", exposure: "on-demand", exposureReason: "hoxi-managed" },
  { name: "built-in-browser", exposure: "unavailable", exposureReason: "missing-tools", missingTools: ["mcp__Claude_Browser__*"] },
  { name: "customize-opencode", exposure: "auto" },
  { name: "us-options-ops", exposure: "on-demand", exposureReason: "hoxi-managed" },
  // review-changes is missing on purpose: skills Hoxi has no decision for stay listed.
];

function systemWith(catalog, runnerGuidance = true) {
  const base = "You are opencode.\nInstructions from: /repo/AGENTS.md\n# Rules\n" + catalog;
  return runnerGuidance ? base + "\nHoxi command execution:\nfenced hoxi-subtasks JSON block" : base;
}

test("rewriteSkillCatalog keeps auto entries, names on-demand ones and drops unavailable ones", () => {
  const text = systemWith(upstreamCatalog(SKILLS));
  const out = rewriteSkillCatalog(text, new Map(EXPOSURES.map((e) => [e.name, e])));
  assert.ok(out.startsWith("You are opencode.\nInstructions from: /repo/AGENTS.md\n# Rules\nSkills provide"));
  assert.ok(out.endsWith("\nHoxi command execution:\nfenced hoxi-subtasks JSON block"));
  for (const kept of ["customize-opencode", "review-changes"]) {
    assert.ok(out.includes(`<description>Long description of ${kept}`), kept);
  }
  for (const gone of ["amazon-ops", "us-options-ops", "built-in-browser"]) {
    assert.ok(!out.includes(`<description>Long description of ${gone}`), gone);
    assert.ok(!out.includes(`<name>${gone}</name>`), gone);
  }
  assert.ok(!out.includes("built-in-browser"), "unavailable skills are not even named");
  assert.match(out, /On-demand skills, listed by name only: amazon-ops, us-options-ops\. Load one with the skill tool only when the user names it/);
  assert.equal(rewriteSkillCatalog("no catalog here", new Map()), null);
  assert.equal(rewriteSkillCatalog(text, new Map()), text, "no decisions leave the catalog unchanged");
});

test("plugin rewrites the catalog from Hoxi's decisions, stays stable and guards loading", async (t) => {
  const previous = { fetch: globalThis.fetch, url: process.env.HOXI_GATEWAY_URL };
  t.after(() => {
    globalThis.fetch = previous.fetch;
    if (previous.url === undefined) delete process.env.HOXI_GATEWAY_URL;
    else process.env.HOXI_GATEWAY_URL = previous.url;
  });
  process.env.HOXI_GATEWAY_URL = "http://127.0.0.1:9100/";
  let calls = [];
  let reply = () => Response.json({ skills: EXPOSURES });
  globalThis.fetch = async (url) => {
    calls.push(String(url));
    return reply();
  };
  const hooks = await BrowserPlugin({ directory: "/repo/.hoxi/worktrees/ses_a" });
  const transform = (system) => hooks["experimental.chat.system.transform"]({ sessionID: "ses_a" }, { system });

  await t.test("catalog is filtered with the plugin instance's directory", async () => {
    const system = [systemWith(upstreamCatalog(SKILLS))];
    await transform(system);
    assert.equal(calls.length, 1);
    const url = new URL(calls[0]);
    assert.equal(url.pathname, "/api/hoxi/skills/list");
    assert.equal(url.searchParams.get("directory"), "/repo/.hoxi/worktrees/ses_a");
    assert.ok(!system[0].includes("Long description of amazon-ops"));
    assert.ok(system[0].includes("On-demand skills, listed by name only: amazon-ops, us-options-ops."));
  });

  await t.test("later turns reuse the decision and produce identical bytes without refetching", async () => {
    const first = [systemWith(upstreamCatalog(SKILLS))];
    const second = [systemWith(upstreamCatalog(SKILLS))];
    await transform(first);
    await transform(second);
    assert.equal(calls.length, 1);
    assert.deepEqual(first, second);
    assert.equal(first.length, 2, "one Hoxi rules block per request");
    assert.equal(first.filter((s) => s.includes("On-demand skills")).length, 1);
  });

  await t.test("runner guidance suppresses the duplicate fallback guidance", async () => {
    const withRunner = [systemWith(upstreamCatalog(SKILLS), true)];
    await transform(withRunner);
    assert.ok(!withRunner[1].includes("hoxi-subtasks"), "runner already carries the subtask protocol");
    assert.ok(!withRunner[1].includes("hoxi_code_index"));
    const subagent = [systemWith(upstreamCatalog(SKILLS), false)];
    await transform(subagent);
    assert.ok(subagent[1].includes("hoxi-subtasks"));
    assert.match(subagent[1], /Reviews and questions are read-only/);
    assert.ok(!/-> edit ->/.test(subagent[1]), "navigation no longer ends in an unconditional edit");
  });

  await t.test("desktop input rules match the real approval gate", async () => {
    const system = [systemWith(upstreamCatalog(SKILLS))];
    await transform(system);
    assert.match(system[1], /one-time approval on the desktop unless the user turned on continuous input/);
    assert.match(system[1], /unattended mode refuses desktop input/);
    assert.ok(!system[1].includes("Every input waits"), "the contradicting wording is gone");
  });

  await t.test("a new upstream catalog (instance reload) is decided again", async () => {
    const system = [systemWith(upstreamCatalog([...SKILLS, "new-skill"]))];
    await transform(system);
    assert.equal(calls.length, 2);
    assert.ok(system[0].includes("<name>new-skill</name>"));
  });

  await t.test("skill tool refuses unavailable skills and allows on-demand ones", async () => {
    const guard = hooks["tool.execute.before"];
    await assert.rejects(guard({ tool: "skill" }, { args: { name: "built-in-browser" } }), /needs tools this runtime does not provide \(mcp__Claude_Browser__\*\)/);
    await guard({ tool: "skill" }, { args: { name: "us-options-ops" } });
    await guard({ tool: "skill" }, { args: { name: "review-changes" } });
    await guard({ tool: "read" }, { args: { filePath: "/x" } });
  });

  await t.test("gateway failure keeps the upstream catalog and backs off", async () => {
    const fresh = await BrowserPlugin({ directory: "/repo" });
    calls = [];
    reply = () => new Response("down", { status: 502 });
    const raw = systemWith(upstreamCatalog(SKILLS));
    const system = [raw];
    await fresh["experimental.chat.system.transform"]({ sessionID: "ses_b" }, { system });
    assert.equal(system[0], raw);
    await fresh["experimental.chat.system.transform"]({ sessionID: "ses_b" }, { system: [raw] });
    assert.equal(calls.length, 1, "a failed decision is not retried on every request");
  });
});
