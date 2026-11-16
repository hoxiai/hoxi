import { createSyntaxHighlightClient } from "../components/timeline/syntax-highlight-client";
import type { HighlightWorkerRequest, HighlightWorkerResponse } from "../components/timeline/syntax-highlight";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

class HighlightWorkerStub {
  static instances: HighlightWorkerStub[] = [];
  onmessage: ((event: MessageEvent<HighlightWorkerResponse>) => void) | null = null;
  onerror: ((event: ErrorEvent) => void) | null = null;
  messages: HighlightWorkerRequest[] = [];
  terminated = false;
  constructor() { HighlightWorkerStub.instances.push(this); }
  postMessage(message: HighlightWorkerRequest) { this.messages.push(message); }
  terminate() { this.terminated = true; }
  emit(message: HighlightWorkerResponse) { this.onmessage?.({ data: message } as MessageEvent<HighlightWorkerResponse>); }
  latest() { return this.messages.filter((message) => message.type === "highlight").at(-1)!; }
}

async function withHighlightWorker(run: () => Promise<void>) {
  const original = Object.getOwnPropertyDescriptor(globalThis, "Worker");
  HighlightWorkerStub.instances = [];
  Object.defineProperty(globalThis, "Worker", { configurable: true, writable: true, value: HighlightWorkerStub });
  try { await run(); } finally {
    if (original) Object.defineProperty(globalThis, "Worker", original);
    else Reflect.deleteProperty(globalThis, "Worker");
  }
}

describe("syntax highlight worker lifecycle", () => {
  it("ignores superseded replies and terminates the worker after the last client disposes", async () => {
    await withHighlightWorker(async () => {
      const client = createSyntaxHighlightClient();
      try {
        const first = client.highlight("const client_old_unique = 1;", "typescript", "dark");
        const worker = HighlightWorkerStub.instances[0];
        const old = worker.latest();
        const second = client.highlight("const client_new_unique = 2;", "typescript", "light");
        const latest = worker.latest();
        assertEqual(await first, undefined);
        let settled = false;
        void second.then(() => { settled = true; });
        worker.emit({ clientID: old.clientID, requestID: old.requestID, result: { code: old.code, tokens: [] } });
        await Promise.resolve();
        assertEqual(settled, false, "old reply resolved the new request");
        worker.emit({ clientID: latest.clientID, requestID: latest.requestID, result: { code: latest.code, tokens: [] } });
        assertEqual((await second)?.code, latest.code);
        client.dispose();
        assertTrue(worker.terminated);
      } finally { client.dispose(); }
    });
  });

  it("settles cancelled and capacity-evicted jobs without retaining timers or clients", async () => {
    await withHighlightWorker(async () => {
      const clients = Array.from({ length: 65 }, () => createSyntaxHighlightClient());
      try {
        const requests = clients.map((client, i) => client.highlight(`const queue_unique_${i} = ${i};`, "typescript", "dark"));
        assertEqual(await requests[0], undefined, "bounded queue did not settle its oldest job");
        const worker = HighlightWorkerStub.instances[0];
        assertTrue(worker.messages.some((message) => message.type === "cancel"));
        for (const client of clients) client.dispose();
        assertTrue((await Promise.all(requests)).every((result) => result === undefined));
        assertTrue(worker.terminated);
        const late = worker.latest();
        worker.emit({ clientID: late.clientID, requestID: late.requestID, result: { code: late.code, tokens: [] } });
      } finally { for (const client of clients) client.dispose(); }
    });
  });

  it("does not start a worker for plain or oversized text", async () => {
    await withHighlightWorker(async () => {
      const client = createSyntaxHighlightClient();
      try {
        assertEqual(await client.highlight("plain", "text", "dark"), undefined);
        assertEqual(await client.highlight("x".repeat(50_001), "typescript", "dark"), undefined);
        assertEqual(HighlightWorkerStub.instances.length, 0);
      } finally { client.dispose(); }
    });
  });

  it("settles worker failures and backs off instead of leaving a card pending", async () => {
    await withHighlightWorker(async () => {
      const client = createSyntaxHighlightClient();
      try {
        const request = client.highlight("const failure_unique_case = 1;", "typescript", "dark");
        const worker = HighlightWorkerStub.instances[0];
        worker.onerror?.({ preventDefault() {} } as ErrorEvent);
        assertEqual(await request, undefined);
        assertTrue(worker.terminated);
        assertEqual(await client.highlight("const failure_unique_case = 2;", "typescript", "dark"), undefined);
        assertEqual(HighlightWorkerStub.instances.length, 1, "worker failure entered a restart loop");
      } finally { client.dispose(); }
    });
  });
});
