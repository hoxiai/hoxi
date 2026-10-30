import { browserClipboard } from "./clipboard.js";
import { observeDiagnostics } from "./diagnostics.js";
import { allowedURL, decodeConnectionCode, snapshot } from "./protocol.js";
import { beginNavigation, finishNavigationCommand, stopNavigation, observeNavigation, cachedNavigationState } from "./navigation.js";

let current = null;
let savedEndpoint = null;
const send = (link, method, params = {}) => chrome.debugger.sendCommand({ tabId: link.tabID }, method, params);

async function loadSavedEndpoint() {
  if (savedEndpoint) return savedEndpoint;
  try {
    const stored = await chrome.storage?.local?.get?.("hoxiEndpoint");
    if (stored?.hoxiEndpoint?.url && stored?.hoxiEndpoint?.token) {
      savedEndpoint = stored.hoxiEndpoint;
    }
  } catch { /* storage unavailable */ }
  return savedEndpoint;
}

async function persistEndpoint(url, token) {
  if (!url || typeof token !== "string" || token.length !== 64) return;
  savedEndpoint = { url, token };
  try { await chrome.storage?.local?.set?.({ hoxiEndpoint: savedEndpoint }); } catch { /* ignore */ }
}

async function attachTab(link, tabID, title = "", url = "") {
  await chrome.debugger.attach({ tabId: tabID }, "1.3");
  await chrome.debugger.sendCommand({ tabId: tabID }, "Page.enable");
  await chrome.debugger.sendCommand({ tabId: tabID }, "Network.enable");
  await chrome.debugger.sendCommand({ tabId: tabID }, "Runtime.enable");
  const frame = await chrome.debugger.sendCommand({ tabId: tabID }, "Page.getFrameTree");
  const mainFrame = frame?.frameTree?.frame?.id;
  link.tabs.set(tabID, { id: String(tabID), title, url, mainFrame });
  if (link.tabID === tabID) link.mainFrame = mainFrame;
  if (link.viewport) await applyViewport(link, tabID);
}

// Hoxi asks linked tabs to render at its inspector's size, like its own
// browser. Detaching the debugger drops the override, restoring the tab.
function validViewport(a) {
  const size = value => Number.isInteger(value) && value >= 1 && value <= 4096;
  return size(a.width) && size(a.height) && a.width * a.height <= 8388608;
}

async function applyViewport(link, tabID) {
  const target = { tabId: tabID };
  if (!link.viewport) return chrome.debugger.sendCommand(target, "Emulation.clearDeviceMetricsOverride");
  // A zero scale factor keeps the screen's own pixel density.
  return chrome.debugger.sendCommand(target, "Emulation.setDeviceMetricsOverride",
    { width: link.viewport.width, height: link.viewport.height, deviceScaleFactor: 0, mobile: false });
}

async function setViewport(link, viewport) {
  link.viewport = viewport;
  for (const id of link.tabs.keys()) {
    try { await applyViewport(link, id); }
    catch (error) { if (id === link.tabID) throw error; }
  }
}

async function disconnect(link = current) {
  if (!link || link.closed) return;
  link.closed = true;
  clearInterval(link.heartbeat);
  link.socket?.close();
  const ids = link.tabs ? [...link.tabs.keys()] : [link.tabID];
  for (const id of ids) {
    try { await chrome.debugger.detach({ tabId: id }); } catch { /* already detached */ }
  }
  if (current === link) current = null;
}

async function evaluate(link, expression) {
  const result = await send(link, "Runtime.evaluate", { expression, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || "Page JavaScript failed");
  return result.result.value;
}

async function state(link) {
  const tab = await chrome.tabs.get(link.tabID);
  // activeTab metadata access can expire after a cross-origin navigation;
  // the explicitly attached debugger remains bound to the selected tab.
  const metadata = await evaluate(link, "({url:location.href,title:document.title})");
  const existing = link.tabs.get(link.tabID) || {};
  link.tabs.set(link.tabID, { ...existing, id: String(tab.id), title: metadata.title || "", url: metadata.url || "" });
  const tabs = [...link.tabs.values()].map(({ id, title, url }) => ({ id, title, url }));
  link.lastState = { url: metadata.url || "", title: metadata.title || "", tabID: String(tab.id), tabs };
  return cachedNavigationState(link);
}

async function navigate(link, a) {
  if (a.action === "navigate" && !allowedURL(a.url)) throw new Error("Only HTTP(S) navigation is supported");
  const generation = beginNavigation(link, a.url || link.lastState?.url || "");
  const command = async () => {
    if (a.action === "navigate") return send(link, "Page.navigate", { url: a.url });
    if (a.action === "reload") return send(link, "Page.reload");
    const history = await send(link, "Page.getNavigationHistory");
    const entry = history.entries[history.currentIndex + (a.action === "back" ? -1 : 1)];
    if (entry) return send(link, "Page.navigateToHistoryEntry", { entryId: entry.id });
    link.navigation.status = "ready";
  };
  if (a.async) {
    void command().then(result => finishNavigationCommand(link, generation, result), error => finishNavigationCommand(link, generation, null, error));
    return cachedNavigationState(link);
  }
  try { const result = await command(); finishNavigationCommand(link, generation, result); }
  catch (error) { finishNavigationCommand(link, generation, null, error); throw error; }
  return state(link);
}

async function key(link, chord) {
  const parts = chord === "+" ? ["+"] : chord.endsWith("++") ? [...chord.slice(0, -2).split("+").filter(Boolean), "+"] : chord.split("+");
  const name = parts.pop();
  let modifiers = 0;
  for (const part of parts) {
    const flag = { Alt: 1, Control: 2, Meta: 4, Shift: 8 }[part];
    if (!flag || (modifiers & flag)) throw new Error("Invalid key modifier");
    modifiers |= flag;
  }
  const codes = { Enter: 13, Tab: 9, Backspace: 8, Escape: 27, Delete: 46,
    ArrowLeft: 37, ArrowUp: 38, ArrowRight: 39, ArrowDown: 40, Home: 36, End: 35,
    PageUp: 33, PageDown: 34, Space: 32 };
  const key = name === "Space" ? " " : name;
  const virtual = codes[name] || (key.length === 1 ? key.toUpperCase().charCodeAt(0) : 0);
  if (!virtual) throw new Error("Unsupported key");
  const params = { key, modifiers, windowsVirtualKeyCode: virtual };
  try {
    await send(link, "Input.dispatchKeyEvent", { ...params, type: "keyDown",
      ...(modifiers === 0 && (key.length === 1 || key === "Enter") ? { text: key === "Enter" ? "\r" : key } : {}),
      ...((modifiers === 2 || modifiers === 4) && key.toLowerCase() === "a" ? { commands: ["selectAll"] } : {}) });
  } finally { await send(link, "Input.dispatchKeyEvent", { ...params, type: "keyUp", modifiers: 0 }); }
}

const MOUSE_TYPES = new Set(["mousePressed", "mouseMoved", "mouseReleased"]);
const MOUSE_BUTTONS = new Set(["none", "left", "middle", "right"]);

// Hoxi tracks held buttons and sends complete CDP mouse events; only forward
// well-formed ones.
async function pointer(link, a) {
  const bounded = (value, max) => Number.isInteger(value) && value >= 0 && value <= max;
  const coordinate = value => Number.isFinite(value) && value >= 0 && value <= 16384;
  if (!MOUSE_TYPES.has(a.type) || !MOUSE_BUTTONS.has(a.button) || !coordinate(a.x) || !coordinate(a.y) ||
    !bounded(a.buttons, 7) || !bounded(a.clickCount, 3) || !bounded(a.modifiers, 15)) throw new Error("Invalid pointer input");
  await send(link, "Input.dispatchMouseEvent", { type: a.type, x: a.x, y: a.y, button: a.button,
    buttons: a.buttons, clickCount: a.clickCount, modifiers: a.modifiers });
}

async function execute(link, a) {
  if (current !== link) throw new Error("Chrome connection ended");
  if (a.action === "tab_select") {
    if (a.tabID === String(link.tabID)) return state(link);
    const targetID = Number(a.tabID);
    if (!link.tabs.has(targetID)) {
      throw new Error("Chrome Link controls only the selected tab; choose another tab in the extension");
    }
    link.tabID = targetID;
    link.mainFrame = link.tabs.get(targetID)?.mainFrame;
    await chrome.tabs.update?.(targetID, { active: true });
    return state(link);
  }
  if (a.action === "tab_new") {
    const nextURL = a.url || "about:blank";
    if (!allowedURL(nextURL)) throw new Error("Only HTTP(S) navigation is supported");
    if (typeof chrome.tabs?.create !== "function") {
      throw new Error("Chrome Link controls only the selected tab; choose another tab in the extension");
    }
    const created = await chrome.tabs.create({ url: nextURL, active: true });
    await attachTab(link, created.id, created.title || "", created.url || nextURL);
    link.tabID = created.id;
    link.mainFrame = link.tabs.get(created.id)?.mainFrame;
    return state(link);
  }
  if (a.action === "tab_close") {
    const targetID = Number(a.tabID);
    if (targetID === link.rootTabID || !link.tabs.has(targetID) || typeof chrome.tabs?.remove !== "function") {
      throw new Error("Chrome Link controls only the selected tab; choose another tab in the extension");
    }
    link.tabs.delete(targetID);
    try { await chrome.debugger.detach({ tabId: targetID }); } catch { /* already detached */ }
    await chrome.tabs.remove(targetID);
    if (link.tabID === targetID) {
      link.tabID = link.rootTabID;
      link.mainFrame = link.tabs.get(link.rootTabID)?.mainFrame;
    }
    return state(link);
  }
  switch (a.action) {
    case "state": break;
    case "focus": {
      const tab = await chrome.tabs.get(link.tabID);
      await chrome.tabs.update?.(link.tabID, { active: true });
      if (tab?.windowId !== undefined) await chrome.windows?.update?.(tab.windowId, { focused: true });
      break;
    }
    case "diagnostics":
      if (a.clear) link.diagnostics = [];
      return { diagnostics: link.diagnostics || [] };
    case "zoom_reset": await send(link, "Emulation.setPageScaleFactor", { pageScaleFactor: 1 }); break;
    case "resize":
      if (!validViewport(a)) throw new Error("Invalid viewport size");
      await setViewport(link, { width: a.width, height: a.height }); break;
    case "viewport_reset": await setViewport(link, null); break;
    case "navigate": case "back": case "forward": case "reload": return navigate(link, a);
    case "stop_loading":
      stopNavigation(link);
      await send(link, "Page.stopLoading");
      return cachedNavigationState(link);
    case "click": {
      let point = { x: a.x || 0, y: a.y || 0 };
      if (a.selector) {
        point = await evaluate(link, `(()=>{const e=document.querySelector(${JSON.stringify(a.selector)});if(!e)throw new Error('Element not found');e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
      }
      await send(link, "Input.dispatchMouseEvent", { type: "mousePressed", ...point, button: "left", clickCount: 1 });
      await send(link, "Input.dispatchMouseEvent", { type: "mouseReleased", ...point, button: "left", clickCount: 1 });
      break;
    }
    case "context": case "copy": case "paste": {
      if (![a.x ?? 0, a.y ?? 0].every(value => Number.isFinite(value) && value >= 0 && value <= 16384) ||
        (a.action === "paste" && (typeof a.text !== "string" || a.text.length > 65536))) throw new Error("Invalid clipboard input");
      const result = await evaluate(link, `(${browserClipboard.toString()})(${JSON.stringify(a.action)},${a.x ?? 0},${a.y ?? 0})`);
      if (a.action === "paste" && !result.clipboardError) await send(link, "Input.insertText", { text: a.text });
      return { ...await state(link), ...result };
    }
    case "type":
      if (a.selector) await evaluate(link, `(()=>{const e=document.querySelector(${JSON.stringify(a.selector)});if(!e)throw new Error('Element not found');e.focus()})()`);
      await send(link, "Input.insertText", { text: a.text || "" }); break;
    case "key": await key(link, a.key); break;
    case "pointer": await pointer(link, a); break;
    case "zoom": {
      if (![a.x, a.y, a.scaleFactor].every(Number.isFinite) || a.x < 0 || a.y < 0 || a.x > 16384 || a.y > 16384 || a.scaleFactor < .25 || a.scaleFactor > 4) throw new Error("Invalid zoom input");
      await send(link, "Input.synthesizePinchGesture", { x: a.x, y: a.y, scaleFactor: a.scaleFactor, gestureSourceType: "mouse" });
      break;
    }
    case "scroll": await send(link, "Input.dispatchMouseEvent", { type: "mouseWheel", x: a.x || 0, y: a.y || 0, deltaX: a.deltaX || 0, deltaY: a.deltaY || 0 }); break;
    case "snapshot": return { ...await state(link), snapshot: await evaluate(link, `(${snapshot.toString()})()`) };
    case "evaluate": {
      const result = await evaluate(link, `(()=>{const v=(0,eval)(${JSON.stringify(a.text)});const s=JSON.stringify(v);return (s===undefined?'undefined':s).slice(0,65536)})()`);
      return { ...await state(link), result };
    }
    case "screenshot": {
      const { width, height, scale, x, y } = await evaluate(link, "({width:innerWidth,height:innerHeight,scale:devicePixelRatio,x:scrollX,y:scrollY})");
      if (width < 1 || height < 1 || width * height > 8388608) throw new Error("Chrome viewport exceeds capture budget");
      const shot = await send(link, "Page.captureScreenshot", { format: "jpeg", quality: 80, captureBeyondViewport: false,
        clip: { x, y, width, height, scale: 1 / Math.max(1, scale) } });
      return { ...await state(link), image: shot.data, width, height };
    }
    default: throw new Error("Unsupported Chrome action");
  }
  return state(link);
}

async function resolveConnection(code) {
  if (typeof code === "string" && code.trim()) return decodeConnectionCode(code.trim());
  const saved = await loadSavedEndpoint();
  if (saved?.url && saved?.token) return saved;
  throw new Error("Paste a Hoxi connection code for the first pairing");
}

async function connect(code, tabID) {
  if (current) throw new Error("Disconnect the existing Chrome Link first");
  const { url, token } = await resolveConnection(code);
  const tab = await chrome.tabs.get(tabID);
  if (!allowedURL(tab.url) || tab.url === "about:blank") throw new Error("Open an HTTP(S) page before connecting");
  const link = { rootTabID: tabID, tabID, tabs: new Map(), socket: null, heartbeat: null };
  current = link;
  try {
    await attachTab(link, tabID, tab.title || "", tab.url);
    link.lastState = { url: tab.url, title: tab.title || "", tabID: String(tabID), tabs: [{ id: String(tabID), url: tab.url, title: tab.title || "" }] };
    if (current !== link) throw new Error("Chrome connection cancelled");
    await new Promise((resolve, reject) => {
      const socket = new WebSocket(url);
      link.socket = socket;
      const timer = setTimeout(() => { reject(new Error("Hoxi handshake timed out")); socket.close(); }, 6000);
      let connected = false;
      let queue = Promise.resolve();
      // Features tell Hoxi which actions this extension version understands.
      socket.onopen = () => socket.send(JSON.stringify({ token, features: ["pointer", "zoom", "navigation", "clipboard", "developer", "viewport"] }));
      socket.onerror = () => reject(new Error("Cannot reach local Hoxi"));
      socket.onclose = () => { clearTimeout(timer); if (!connected) reject(new Error("Connection code expired or rejected")); void disconnect(link); };
      socket.onmessage = event => {
        if (!connected) {
          const hello = JSON.parse(event.data);
          if (!hello.connected) { reject(new Error("Invalid Hoxi handshake")); return; }
          connected = true; clearTimeout(timer);
          if (typeof hello.linkToken === "string" && hello.linkToken.length === 64) {
            void persistEndpoint(url, hello.linkToken);
          }
          link.heartbeat = setInterval(() => { if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ heartbeat: true })); }, 20000);
          resolve(); return;
        }
        queue = queue.then(async () => {
          try { const result = await execute(link, JSON.parse(event.data)); if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ result })); }
          catch (error) { if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ error: error.message })); }
        });
      };
    });
  } catch (error) { await disconnect(link); throw error; }
}

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  // No content script or external messaging endpoint exists. Only the popup can connect.
  if (sender.id !== chrome.runtime.id || sender.url !== chrome.runtime.getURL("popup.html")) return false;
  if (message.type === "status") {
    void loadSavedEndpoint().then(saved => respond({
      ok: true,
      connected: Boolean(current && !current.closed),
      hasSavedEndpoint: Boolean(saved?.url && saved?.token),
    }));
    return true;
  }
  const result = message.type === "connect" ? connect(message.code, message.tabID) :
    message.type === "disconnect" ? disconnect() : Promise.reject(new Error("Unknown request"));
  result.then(() => respond({ ok: true }), error => respond({ error: error.message }));
  return true;
});
chrome.debugger.onDetach.addListener(source => {
  if (!current) return;
  if (current.rootTabID === source.tabId || current.tabs.size <= 1) {
    void disconnect(current);
  } else if (current.tabs.has(source.tabId)) {
    current.tabs.delete(source.tabId);
    if (current.tabID === source.tabId) current.tabID = current.rootTabID;
  }
});
chrome.tabs?.onCreated?.addListener(tab => {
  if (!current || current.closed || !tab?.id || !tab.openerTabId || !current.tabs.has(tab.openerTabId)) return;
  const nextURL = tab.pendingUrl || tab.url || "about:blank";
  if (!allowedURL(nextURL)) return;
  void attachTab(current, tab.id, tab.title || "", nextURL).then(() => {
    if (current && !current.closed && current.tabs.has(tab.id)) {
      current.tabID = tab.id;
      current.mainFrame = current.tabs.get(tab.id)?.mainFrame;
    }
  }).catch(() => { /* ignore restricted tab */ });
});
chrome.debugger.onEvent?.addListener((source, method, params) => {
  if (current?.tabID === source.tabId) { observeNavigation(current, method, params); observeDiagnostics(current, method, params); }
});
