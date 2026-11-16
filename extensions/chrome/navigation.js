const code = error => String(error?.message || error).match(/\bnet::ERR_[A-Z0-9_]{1,80}\b/)?.[0] || "navigation_failed";

export function beginNavigation(link, url) {
  link.navigationGeneration = (link.navigationGeneration || 0) + 1;
  link.navigation = { status: "loading", url, startedAt: Date.now() };
  link.documentRequest = null;
  link.documentFinished = false;
  return link.navigationGeneration;
}

export function finishNavigationCommand(link, generation, result, error) {
  if (link.closed || generation !== link.navigationGeneration) return;
  if (error || result?.errorText) link.navigation = { ...link.navigation, status: "failed", error: code(error || result.errorText) };
}

export function stopNavigation(link) {
  link.navigationGeneration = (link.navigationGeneration || 0) + 1;
  link.navigation = { ...link.navigation, status: "stopped", error: "" };
}

export function observeNavigation(link, method, params) {
  const nav = link.navigation || { status: "ready" };
  if (method === "Page.frameNavigated" && !params.frame.parentId) {
    link.mainFrame = params.frame.id;
    if (!params.frame.url.startsWith("chrome-error:")) link.navigation = { ...nav, url: params.frame.url };
  } else if (method === "Page.frameStartedNavigating" && params.frameId === link.mainFrame) {
    if (nav.status !== "loading") beginNavigation(link, params.url);
    else link.navigation.url = params.url;
  } else if (method === "Network.requestWillBeSent" && params.frameId === link.mainFrame && params.type === "Document") {
    link.documentRequest = params.requestId;
    link.navigation = { ...nav, url: params.request.url };
  } else if (method === "Network.responseReceived" && params.requestId === link.documentRequest) {
    link.navigation = { ...nav, httpStatus: params.response.status };
  } else if (method === "Network.loadingFailed" && params.requestId === link.documentRequest && nav.status !== "stopped") {
    link.navigation = { ...nav, status: "failed", error: code(params.errorText) };
  } else if (method === "Network.loadingFinished" && params.requestId === link.documentRequest) {
    link.documentFinished = true;
  } else if (method === "Page.loadEventFired" && link.documentFinished && nav.status === "loading") {
    link.navigation = { ...nav, status: nav.httpStatus >= 400 ? "failed" : "ready" };
  } else if (method === "Page.navigatedWithinDocument" && params.frameId === link.mainFrame) {
    link.navigation = { ...nav, status: "ready", url: params.url };
  }
}

export function cachedNavigationState(link) {
  const prior = link.lastState || { url: "", title: "", tabID: String(link.tabID), tabs: [{ id: String(link.tabID), url: "", title: "" }] };
  return { ...prior, navigation: { ...(link.navigation || { status: "ready" }) }, navigationControls: true };
}
