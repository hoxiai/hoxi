// web/scripts/run-tests.ts
import fs from "node:fs";
import path from "node:path";
import "../src/__tests__/i18n.test";
import "../src/__tests__/sse.test";
import "../src/__tests__/sse-reconnect.test";
import "../src/__tests__/provider-health.test";
import "../src/__tests__/tool-card-display.test";
import "../src/__tests__/provider-selection.test";
import "../src/__tests__/provider-catalog.test";
import "../src/__tests__/runtime-switch.test";
import "../src/__tests__/permissions.test";
import "../src/__tests__/runner-permission.test";
import "../src/__tests__/memory.test";
import "../src/__tests__/merge-turns.test";
import "../src/__tests__/history-paging.test";
import "../src/__tests__/task-history.test";
import "../src/__tests__/context-mention-loading.test";
import "../src/__tests__/task-progress.test";
import "../src/__tests__/changed-files-scratch.test";
import "../src/__tests__/question-answers.test";
import "../src/__tests__/session-isolation.test";
import "../src/__tests__/project-attribution.test";
import "../src/__tests__/workspace-shared-folders.test";
import "../src/__tests__/session-lists.test";
import "../src/__tests__/worktree-refresh.test";
import "../src/__tests__/new-thread.test";
import "../src/__tests__/runner-sandbox.test";
import "../src/__tests__/session-ai.test";
import "../src/__tests__/old-sessions.test";
import "../src/__tests__/git-commit.test";
import "../src/__tests__/daily-pulse-cost.test";
import "../src/__tests__/usage-history.test";
import "../src/__tests__/format-units.test";
import "../src/__tests__/session-title.test";
import "../src/__tests__/timeline-scroll.test";
import "../src/__tests__/session-scroll.test";
import "../src/__tests__/reading-position.test";
import "../src/__tests__/transcript-controls.test";
import "../src/__tests__/session-history.test";
import "../src/__tests__/cross-project-nav.test";
import "../src/__tests__/cross-project-status.test";
import "../src/__tests__/deep-link.test";
import "../src/__tests__/task-overview.test";
import "../src/__tests__/session-search.test";
import "../src/__tests__/syntax-highlight.test";
import "../src/__tests__/syntax-highlight-client.test";
import "../src/__tests__/markdown-image.test";
import "../src/__tests__/markdown-performance.test";
import "../src/__tests__/stream-reveal.test";
import "../src/__tests__/image-viewer.test";
import "../src/__tests__/image-memory.test";
import "../src/__tests__/composer-keyboard.test";
import "../src/__tests__/notification-sound.test";
import { runAllTests } from "../src/__tests__/test-harness";
import "../src/__tests__/browser.test";
import "../src/__tests__/browser-input.test";
import "../src/__tests__/browser-clipboard.test";
import "../src/__tests__/browser-workflows.test";
import "../src/__tests__/computer.test";
import "../src/__tests__/terminal-lifecycle.test";
import "../src/__tests__/diagnostics.test";
import "../src/__tests__/appearance.test";
import "../src/__tests__/network.test";
import "../src/__tests__/telemetry.test";
import "../src/__tests__/local-relay.test";
import "../src/__tests__/pairing.test";
import "../src/__tests__/cloudflared.test";
import "../src/__tests__/auto-models.test";
import "../src/__tests__/auto-breaker.test";
import "../src/__tests__/auto-failover.test";
import "../src/__tests__/session-error-recovery.test";
import "../src/__tests__/auto-candidate-status.test";
import "../src/__tests__/auto-session-affinity.test";
import "../src/__tests__/session-goal.test";
import "../src/__tests__/skills.test";
import "../src/__tests__/context-mentions.test";
import "../src/__tests__/session-capabilities.test";
import "../src/__tests__/session-revert.test";
import "../src/__tests__/turn-window.test";
import "../src/__tests__/session-transcripts.test";
import "../src/__tests__/internal-llm-model.test";
import "../src/__tests__/subagent-config.test";
import "../src/__tests__/model-limits.test";
import "../src/__tests__/updater-state.test";
import "../src/__tests__/auth-state.test";
import "../src/__tests__/official-channel.test";
import "../src/__tests__/surface-onboarding.test";
import "../src/__tests__/layout.test";
import "../src/__tests__/gemini-image-session.test";
import "../src/__tests__/slash-and-agents.test";
import "../src/__tests__/context-watermark.test";
import "../src/__tests__/request-ledger.test";
import "../src/__tests__/request-recovery.test";
import "../src/__tests__/plan-consent.test";
import "../src/__tests__/intake.test";
import "../src/__tests__/fast-mode.test";
import "../src/__tests__/media-loopback.test";
import "../src/__tests__/desktop-window.test";
import "../src/__tests__/gateway-directory.test";
import "../src/__tests__/orchestrator-mode.test";
import "../src/__tests__/schedules.test";
import "../src/__tests__/context-protocol.test";
import "../src/__tests__/display-layer.test";
import "../src/__tests__/rich-views.test";
import "../src/__tests__/model-capabilities.test";
import "../src/__tests__/share-image-model.test";

// Guard against orphaned unit tests: every src/__tests__/*.test.{ts,tsx} file
// must be imported above so newly added suites cannot silently go unexecuted.
{
  const scriptPath = path.resolve(process.cwd(), process.argv[2] || "scripts/run-tests.ts");
  const testDir = path.resolve(path.dirname(scriptPath), "../src/__tests__");
  if (fs.existsSync(scriptPath) && fs.existsSync(testDir)) {
    const runnerSrc = fs.readFileSync(scriptPath, "utf8");
    const orphans = fs
      .readdirSync(testDir)
      .filter((name) => /\.test\.tsx?$/.test(name))
      .sort()
      .filter((name) => {
        const stem = name.replace(/\.tsx?$/, "");
        return !runnerSrc.includes(`/${stem}"`) && !runnerSrc.includes(`/${stem}'`);
      });
    if (orphans.length > 0) {
      console.error(
        `[test] ✗ Orphaned frontend test file(s) not registered in web/scripts/run-tests.ts: ${orphans.join(", ")}`
      );
      process.exit(1);
    }
  }
}

console.log("[test] Running Hoxi frontend unit tests...");
runAllTests().then((failed) => {
  // Exit explicitly: store modules under test leave long timers (debounce/TTL)
  // armed, which would otherwise keep Node alive for seconds after the last
  // test. Flush stdout first so piped output is not truncated.
  process.stdout.write("", () => process.exit(failed > 0 ? 1 : 0));
});
