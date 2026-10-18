// web/scripts/run-tests.ts
import "../src/__tests__/i18n.test";
import "../src/__tests__/sse.test";
import "../src/__tests__/provider-health.test";
import "../src/__tests__/permissions.test";
import "../src/__tests__/memory.test";
import "../src/__tests__/merge-turns.test";
import "../src/__tests__/task-progress.test";
import "../src/__tests__/session-isolation.test";
import "../src/__tests__/session-ai.test";
import "../src/__tests__/timeline-scroll.test";
import { runAllTests } from "../src/__tests__/test-harness";

console.log("[test] Running Hoxi frontend unit tests...");
runAllTests().then((failed) => {
  if (failed > 0) {
    process.exit(1);
  }
});
