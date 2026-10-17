// web/scripts/run-tests.ts
import "../src/__tests__/i18n.test";
import "../src/__tests__/sse.test";
import "../src/__tests__/provider-health.test";
import { runAllTests } from "../src/__tests__/test-harness";

console.log("[test] Running Hoxi frontend unit tests...");
runAllTests().then((failed) => {
  if (failed > 0) {
    process.exit(1);
  }
});
