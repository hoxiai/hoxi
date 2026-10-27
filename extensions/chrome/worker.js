import { allowedURL, decodeConnectionCode, snapshot } from "./protocol.js";
import { beginNavigation, finishNavigationCommand, stopNavigation, observeNavigation, cachedNavigationState } from "./navigation.js";

let current = null;
const send = (link, method, params = {}) => chrome.debugger.sendCommand({ tabId: link.tabID }, method, params);

async function disconnect(link = current) {
  if (!link || link.closed) return;
  link.closed = true;
  clearInterval(link.heartbeat);
  link.socket?.close();
  try { await chrome.debugger.detach({ tabId: link.tabID }); } catch { /* already detached */ }
  if (current === link) current = null;
}

async function evaluate(link, expression) {
  const result = await send(link, "Runtime.evaluate", { expression, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || "Page JavaScript failed");
  return result.result.value;
}

async function state(link) {
  // Never enumerate other tabs. The tab chosen in the popup is the entire scope.
  const tab = await chrome.tabs.get(link.tabID);
  // activeTab metadata access can expire after a cross-origin navigation;
  // the explicitly attached debugger remains bound to the same selected tab.
  const metadata = await evaluate(link, "({url:location.href,title:document.title})");
  link.lastState = { url: metadata.url || "", title: metadata.title || "", tabID: String(tab.id),
    tabs: [{ id: String(tab.id), title: metadata.title || "", url: metadata.url || "" }] };
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
  if (a.action === "tab_select" && a.tabID === String(link.tabID)) return state(link);
  if (["tab_new", "tab_select", "tab_close"].includes(a.action)) {
    throw new Error("Chrome Link controls only the selected tab; choose another tab in the extension");
  }
  switch (a.action) {
    case "state": break;
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

async function connect(code, tabID) {
  if (current) throw new Error("Disconnect the existing Chrome Link first");
  const { url, token } = decodeConnectionCode(code);
  const tab = await chrome.tabs.get(tabID);
  if (!allowedURL(tab.url) || tab.url === "about:blank") throw new Error("Open an HTTP(S) page before connecting");
  const link = { tabID, socket: null, heartbeat: null };
  current = link;
  try {
    await chrome.debugger.attach({ tabId: tabID }, "1.3");
    await send(link, "Page.enable");
    await send(link, "Network.enable");
    const frame = await send(link, "Page.getFrameTree");
    link.mainFrame = frame?.frameTree?.frame?.id;
    link.lastState = { url: tab.url, title: tab.title || "", tabID: String(tabID), tabs: [{ id: String(tabID), url: tab.url, title: tab.title || "" }] };
    if (current !== link) throw new Error("Chrome connection cancelled");
    await new Promise((resolve, reject) => {
      const socket = new WebSocket(url);
      link.socket = socket;
      const timer = setTimeout(() => { reject(new Error("Hoxi handshake timed out")); socket.close(); }, 6000);
      let connected = false;
      let queue = Promise.resolve();
      // Features tell Hoxi which actions this extension version understands.
      socket.onopen = () => socket.send(JSON.stringify({ token, features: ["pointer", "zoom", "navigation"] }));
      socket.onerror = () => reject(new Error("Cannot reach local Hoxi"));
      socket.onclose = () => { clearTimeout(timer); if (!connected) reject(new Error("Connection code expired or rejected")); void disconnect(link); };
      socket.onmessage = event => {
        if (!connected) {
          const hello = JSON.parse(event.data);
          if (!hello.connected) { reject(new Error("Invalid Hoxi handshake")); return; }
          connected = true; clearTimeout(timer);
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
  const result = message.type === "connect" ? connect(message.code, message.tabID) :
    message.type === "disconnect" ? disconnect() : Promise.reject(new Error("Unknown request"));
  result.then(() => respond({ ok: true }), error => respond({ error: error.message }));
  return true;
});
chrome.debugger.onDetach.addListener(source => { if (current?.tabID === source.tabId) void disconnect(current); });
chrome.debugger.onEvent?.addListener((source, method, params) => {
  if (current?.tabID === source.tabId) observeNavigation(current, method, params);
});
