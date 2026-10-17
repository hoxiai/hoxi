// web/src/__tests__/test-harness.ts

export interface TestCase {
  suite: string;
  name: string;
  fn: () => void | Promise<void>;
}

const registeredTests: TestCase[] = [];

export function describe(suite: string, registerFn: () => void): void {
  const prevSuite = currentSuite;
  currentSuite = suite;
  try {
    registerFn();
  } finally {
    currentSuite = prevSuite;
  }
}

let currentSuite = "default";

export function it(name: string, fn: () => void | Promise<void>): void {
  registeredTests.push({ suite: currentSuite, name, fn });
}

export function assertEqual<T>(actual: T, expected: T, message?: string): void {
  if (actual !== expected) {
    throw new Error(
      `${message ? message + ": " : ""}expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`
    );
  }
}

export function assertDeepEqual<T>(actual: T, expected: T, message?: string): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) {
    throw new Error(`${message ? message + ": " : ""}expected ${b}, got ${a}`);
  }
}

export function assertTrue(condition: boolean, message?: string): void {
  if (!condition) {
    throw new Error(message || "expected condition to be true");
  }
}

export async function runAllTests(): Promise<number> {
  let passed = 0;
  let failed = 0;

  for (const tc of registeredTests) {
    try {
      await tc.fn();
      passed++;
      console.log(`  ✓ [${tc.suite}] ${tc.name}`);
    } catch (err) {
      failed++;
      const msg = err instanceof Error ? err.message : String(err);
      console.error(`  ✗ [${tc.suite}] ${tc.name}\n      ${msg}`);
    }
  }

  console.log(`\n[test] ${passed} passed, ${failed} failed (${registeredTests.length} total)`);
  return failed;
}
