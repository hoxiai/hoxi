import { test } from "node:test";
import assert from "node:assert/strict";
import { beginNavigation, finishNavigationCommand, observeNavigation, stopNavigation, cachedNavigationState } from "./navigation.js";

test("navigation state keeps stopped/replaced jobs and main-document errors separate", () => {
  const link = { tabID: 42, mainFrame: "main" };
  const old = beginNavigation(link, "https://one.example");
  beginNavigation(link, "https://two.example");
  finishNavigationCommand(link, old, null, new Error("old failure"));
  assert.equal(link.navigation.status, "loading");
  observeNavigation(link, "Network.requestWillBeSent", { frameId: "child", type: "Document", requestId: "iframe", request: { url: "https://child.example" } });
  observeNavigation(link, "Network.loadingFailed", { requestId: "iframe", errorText: "net::ERR_FAILED" });
  assert.equal(link.navigation.status, "loading");
  observeNavigation(link, "Network.requestWillBeSent", { frameId: "main", type: "Document", requestId: "doc", request: { url: "https://two.example" } });
  observeNavigation(link, "Network.responseReceived", { requestId: "doc", response: { status: 503 } });
  observeNavigation(link, "Network.loadingFinished", { requestId: "doc" });
  observeNavigation(link, "Page.loadEventFired", {});
  assert.equal(link.navigation.status, "failed");
  assert.equal(link.navigation.httpStatus, 503);
  stopNavigation(link);
  finishNavigationCommand(link, old, null, new Error("failed later"));
  observeNavigation(link, "Network.loadingFailed", { requestId: "doc", errorText: "net::ERR_ABORTED" });
  assert.equal(cachedNavigationState(link).navigation.status, "stopped");
  assert.equal(cachedNavigationState(link).tabs[0].id, "42");
});

test("navigation errors expose a fixed label or a network code, without page text", () => {
  const link = { tabID: 42 };
  let generation = beginNavigation(link, "https://example.com");
  finishNavigationCommand(link, generation, null, new Error("https://secret.example/credential page controlled text"));
  assert.equal(link.navigation.error, "navigation_failed");
  generation = beginNavigation(link, "https://example.com");
  finishNavigationCommand(link, generation, { errorText: "net::ERR_NAME_NOT_RESOLVED" });
  assert.equal(link.navigation.error, "net::ERR_NAME_NOT_RESOLVED");
});
