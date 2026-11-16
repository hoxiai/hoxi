export function decodeConnectionCode(code) {
  const raw = atob(code.replace(/-/g, "+").replace(/_/g, "/"));
  const url = new URL(raw);
  if (url.protocol !== "http:" || url.hostname !== "127.0.0.1" || !url.port ||
      url.pathname !== "/api/hoxi/browser/extension" || url.search || url.username || url.password ||
      !/^#[a-f0-9]{64}$/.test(url.hash)) throw new Error("Invalid local Hoxi connection code");
  const token = url.hash.slice(1);
  url.hash = "";
  url.protocol = "ws:";
  return { url: url.href, token };
}

export function allowedURL(raw) {
  if (raw === "about:blank") return true;
  try { const u = new URL(raw); return ["https:", "http:"].includes(u.protocol) && !u.username && !u.password; }
  catch { return false; }
}

// Executed in the selected page, with no extension or Hoxi credentials in scope.
export function snapshot() {
  const controls = [];
  function selector(el) {
    const parts = [];
    while (el && el.nodeType === 1) {
      let part = el.localName;
      if (el.id) {
        const id = "#" + CSS.escape(el.id);
        if (document.querySelectorAll(id).length === 1) { parts.unshift(id); break; }
      }
      const parent = el.parentElement;
      if (parent) {
        const peers = [...parent.children].filter(x => x.localName === el.localName);
        if (peers.length > 1) part += `:nth-of-type(${peers.indexOf(el) + 1})`;
      }
      parts.unshift(part); el = parent;
    }
    return parts.join(" > ");
  }
  for (const el of document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"],[tabindex]')) {
    if (controls.length >= 200) break;
    if (!el.getClientRects().length || getComputedStyle(el).visibility === "hidden") continue;
    controls.push({ selector: selector(el).slice(0, 2048), tag: el.localName,
      text: (el.getAttribute("aria-label") || el.innerText || el.getAttribute("placeholder") || "").slice(0, 256) });
  }
  return { text: (document.body?.innerText || "").slice(0, 32768), controls };
}
