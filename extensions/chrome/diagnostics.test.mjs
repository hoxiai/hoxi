import { test } from "node:test";
import assert from "node:assert/strict";
import { diagnosticURL, observeDiagnostics } from "./diagnostics.js";

test("diagnostics bound their records and redact URL credentials, queries and fragments", () => {
  assert.equal(diagnosticURL("https://user:secret@example.com/path?token=secret#private"), "https://example.com/path");
  assert.equal(diagnosticURL("file:///private/secret"), "");
  const link = {};
  observeDiagnostics(link, "Network.requestWillBeSent", { requestId: "one", request: { url: "https://example.com/api?token=secret" } });
  observeDiagnostics(link, "Network.loadingFailed", { requestId: "one", errorText: "page text secret net::ERR_CONNECTION_REFUSED" });
  assert.equal(link.diagnostics[0].message, "net::ERR_CONNECTION_REFUSED");
  assert.equal(link.diagnostics[0].url, "https://example.com/api");
  assert.equal(link.diagnosticRequests.size, 0);
  observeDiagnostics(link, "Runtime.consoleAPICalled", { type: "log", args: [{ type: "string", value: "ignored" }] });
  assert.equal(link.diagnostics.length, 1);
  for (let i = 0; i < 120; i++) observeDiagnostics(link, "Runtime.consoleAPICalled", { type: "error", args: [{ type: "string", value: "x".repeat(5000) }] });
  assert.equal(link.diagnostics.length, 100);
  assert.ok(link.diagnostics.every(item => item.message.length <= 2048));
});
