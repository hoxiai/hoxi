import { test } from "node:test";
import assert from "node:assert/strict";
import { allowedURL, decodeConnectionCode } from "./protocol.js";

const code = raw => Buffer.from(raw).toString("base64url");
test("connection codes permit only the dedicated literal loopback endpoint", () => {
  const token = "a".repeat(64);
  assert.deepEqual(decodeConnectionCode(code(`http://127.0.0.1:5420/api/hoxi/browser/extension#${token}`)),
    { url: "ws://127.0.0.1:5420/api/hoxi/browser/extension", token });
  for (const raw of [
    `http://example.com:5420/api/hoxi/browser/extension#${token}`,
    `http://127.0.0.1:5420/api/hoxi/bootstrap#${token}`,
    `http://user:pass@127.0.0.1:5420/api/hoxi/browser/extension#${token}`,
    `http://127.0.0.1:5420/api/hoxi/browser/extension?q=1#${token}`,
    `http://127.0.0.1/api/hoxi/browser/extension#${token}`,
    `http://127.0.0.1:5420/api/hoxi/browser/extension#short`,
    `https://127.0.0.1:5420/api/hoxi/browser/extension#${token}`,
  ]) assert.throws(() => decodeConnectionCode(code(raw)), raw);
});

test("navigation rejects local files, script URLs and credentials", () => {
  for (const url of ["https://example.com", "http://localhost:3000", "about:blank"]) assert.equal(allowedURL(url), true);
  for (const url of ["file:///etc/passwd", "javascript:alert(1)", "chrome://settings", "data:text/html,x", "https://user:pass@example.com", "bad"])
    assert.equal(allowedURL(url), false);
});
