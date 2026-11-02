import { createRoot, createSignal } from "solid-js";
import { render } from "solid-js/web";
import { unwrap } from "solid-js/store";
import Composer from "../components/timeline/Composer";
import MessageStream from "../components/timeline/MessageStream";
import { createImageAttachments, createPasteCollapse, createSessionDraftSync } from "../components/timeline/composer-hooks";
import { hoxiAPI } from "../services/api";
import { t } from "../i18n";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace-state";
import { dispatchSessionPrompt, drainNextQueuedPrompt, enqueueSessionPrompt } from "../store/workspace";
import { assertEqual, assertTrue } from "./test-harness";
import "../styles.css";

const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
let nextFixture = 0;
function fixture() {
  const sid = `ses_send_${++nextFixture}`, other = `${sid}_other`, ref = `${sid}_reference`;
  const model = { providerID: "probe", modelID: "mini" };
  setWorkspace({ rootDir: "/probe", activeSessionID: sid, connected: true,
    providers: [{ id: "probe", name: "Probe", models: { mini: { id: "mini" } } }],
    sessions: [sid, other, ref].map((id) => ({ id, title: "Send fixture", directory: `/probe/${id}`, time: { created: 1, updated: 1 } })),
  });
  for (const id of [sid, other]) {
    setWorkspace("aiBySession", id, { model, agent: "build", effort: "" });
    setWorkspace("statusBySession", id, { type: "idle" });
    setWorkspace("messagesBySession", id, [{
      info: { id: `msg_prior_${id}`, sessionID: id, role: "user", time: { created: 1 } },
      parts: [{ id: `prt_prior_${id}`, messageID: `msg_prior_${id}`, sessionID: id, type: "text", text: "Previous request" }],
    }]);
  }
  hoxiAPI.getMessages = async (id) => structuredClone(unwrap(workspace.messagesBySession[id] ?? []));
  hoxiAPI.getSessionDiff = async () => [];
  hoxiAPI.getWorktreeDiff = async () => [];
  const reference: MessageTurn[] = [{
    info: { id: `msg_${ref}`, sessionID: ref, role: "assistant", time: { created: 2, completed: 3 } },
    parts: [{ id: `prt_${ref}`, messageID: `msg_${ref}`, sessionID: ref, type: "text", text: "Referenced design details" }],
  }];
  return { sid, other, ref, reference };
}
function composer() {
  const host = document.createElement("div"); host.style.width = "900px"; document.body.append(host);
  const dispose = render(() => <Composer />, host);
  const field = () => host.querySelector("textarea")!;
  return { host, field,
    type: (text: string) => { field().value = text; field().dispatchEvent(new Event("input", { bubbles: true })); },
    send: () => {
      const button = Array.from(host.querySelectorAll("button")).find((item) => item.title.startsWith(`${t("timeline.send")} (`));
      assertTrue(Boolean(button && !button.disabled), "fixture should allow sending"); button!.click();
    }, cleanup: () => { dispose(); host.remove(); },
  };
}

async function testInactiveFailure() {
  const { sid, other } = fixture();
  const request = deferred<void>(); let calls = 0;
  hoxiAPI.sendPromptAsync = async () => { calls++; return request.promise; };
  const c = composer();
  try {
    c.type("A failed draft"); c.send(); await frame();
    c.type("A newer unsent text");
    setWorkspace("activeSessionID", other); await frame(); c.type("B unrelated draft");
    request.reject(new Error("A send failed")); await frame();
    assertEqual(c.field().value, "B unrelated draft");
    assertTrue(!c.host.textContent?.includes("A send failed"), "A's failure must not appear in B");
    setWorkspace("activeSessionID", sid); await frame();
    assertEqual(c.field().value, "A failed draft\n\nA newer unsent text", "both failed and newer text must survive");
    assertTrue(c.host.textContent?.includes("A send failed"));
    hoxiAPI.sendPromptAsync = async () => { calls++; };
    c.send(); await frame();
    assertEqual(calls, 2); assertEqual(c.field().value, "");
    assertTrue(!c.host.textContent?.includes("A send failed"), "successful retry clears the old error");
  } finally { c.cleanup(); }
}

async function testUnmountFailure() {
  fixture(); const request = deferred<void>();
  hoxiAPI.sendPromptAsync = () => request.promise;
  const c = composer(); c.type("Draft sent before unmount"); c.send(); await frame(); c.cleanup();
  request.reject(new Error("Unmounted send failed")); await frame();
  const restored = composer();
  try {
    assertEqual(restored.field().value, "Draft sent before unmount");
    assertTrue(restored.host.textContent?.includes("Unmounted send failed"));
  } finally { restored.cleanup(); }
}

async function testReferencedSend() {
  const { sid, other, ref, reference } = fixture();
  const request = deferred<MessageTurn[]>(); let reads = 0;
  hoxiAPI.getMessages = async (id) => { if (id === ref) { reads++; return request.promise; } return []; };
  setWorkspace("reviewCommentsBySession", sid, [{ id: "review_a", file: "a.ts", line: 1, body: "ONLY_A_REVIEW", snippet: "" }]);
  setWorkspace("reviewCommentsBySession", other, [{ id: "review_b", file: "b.ts", line: 1, body: "ONLY_B_REVIEW", snippet: "" }]);
  const sent: Array<{ id: string; text: string }> = [];
  hoxiAPI.sendPromptAsync = async (id, text) => { sent.push({ id, text }); };
  const c = composer();
  try {
    c.type(`Use @session:${ref}`); c.send(); await frame();
    assertEqual(sent.length, 0, "sending must wait for an already-running preview"); assertEqual(reads, 1);
    setWorkspace("reviewCommentsBySession", sid, (comments) => [...comments, {
      id: "review_a_later", file: "later.ts", line: 1, body: "A review added while loading", snippet: "",
    }]);
    setWorkspace("activeSessionID", other); await frame(); c.type("B draft while A is loading");
    request.resolve(reference); await frame(); await frame();
    assertEqual(sent.length, 1); assertEqual(sent[0].id, sid);
    assertTrue(sent[0].text.includes("Referenced design details") && sent[0].text.includes("ONLY_A_REVIEW"));
    assertTrue(!sent[0].text.includes("ONLY_B_REVIEW"), "reviews must be captured from the originating session");
    assertEqual(c.field().value, "B draft while A is loading");
    assertEqual(workspace.reviewCommentsBySession[other].length, 1);
    assertEqual(workspace.reviewCommentsBySession[sid].length, 1);
    assertEqual(workspace.reviewCommentsBySession[sid][0].id, "review_a_later", "only comments actually sent should be cleared");
  } finally { c.cleanup(); }
}

async function testReferenceFailure() {
  const { ref, reference } = fixture();
  const request = deferred<MessageTurn[]>();
  hoxiAPI.getMessages = () => request.promise;
  let sends = 0; hoxiAPI.sendPromptAsync = async () => { sends++; };
  const c = composer(); const text = `Use @session:${ref}`;
  try {
    c.type(text); c.send(); await frame();
    request.reject(new Error("Reference read failed")); await frame();
    assertEqual(sends, 0, "incomplete context must not be sent to the model");
    assertEqual(c.field().value, text); assertTrue(c.host.textContent?.includes("Reference read failed"));
    hoxiAPI.getMessages = async () => reference;
    c.send(); await frame(); await frame();
    assertEqual(sends, 1, "the user can retry immediately after context recovers");
    assertEqual(c.field().value, "");
  } finally { c.cleanup(); }
}

async function testDraftAttachments() {
  const { sid, other } = fixture();
  await createRoot(async (dispose) => {
    const [text, setText] = createSignal("A draft");
    const images = createImageAttachments(() => workspace.activeSessionID, () => workspace.rootDir);
    const pastes = createPasteCollapse();
    images.replaceImages([{ id: "image_a", filename: "a.png", mime: "image/png", dataUrl: "a.png" }]);
    pastes.replacePastes([{ id: "paste_a", lines: 20, chars: 100, preview: "A paste", content: "Full A paste" }]);
    const sync = createSessionDraftSync({ sessionID: () => workspace.activeSessionID, directory: () => workspace.rootDir,
      text, setText, imgAttach: images, pasteCollapse: pastes, onAfterRestore: () => {} });
    try {
      const draft = { text: text(), images: images.images(), pastes: pastes.pastes() };
      setText(""); images.clearImages(); pastes.clearPastes();
      setWorkspace("activeSessionID", other);
      setText("B draft"); images.replaceImages([{ id: "image_b", filename: "b.png", mime: "image/png", dataUrl: "b.png" }]);
      sync.restoreFailedDraft(draft, sid, "/probe", "A failed");
      assertEqual(text(), "B draft"); assertEqual(images.images()[0].id, "image_b"); assertEqual(images.notice(), null);
      setWorkspace("activeSessionID", sid);
      assertEqual(text(), "A draft"); assertEqual(images.images()[0].id, "image_a"); assertEqual(pastes.pastes()[0].content, "Full A paste");
      assertEqual(images.notice(), "A failed");
      sync.restoreFailedDraft(draft, sid, "/probe", "A failed");
      assertEqual(images.images().length, 1); assertEqual(pastes.pastes().length, 1);
    } finally { dispose(); }
  });
}

async function testQueuedDispatchStaysVisible() {
  const { sid } = fixture();
  hoxiAPI.sendPromptAsync = async () => {};
  const host = document.createElement("div");
  host.style.cssText = "width:900px;height:650px;display:flex;flex-direction:column";
  document.body.append(host);
  const dispose = render(() => <MessageStream />, host);
  const userTurn = (text: string) => Array.from(host.querySelectorAll<HTMLElement>("[data-turn-id]"))
    .find((el) => el.textContent?.includes(text));
  try {
    await frame();
    enqueueSessionPrompt(sid, "Queued follow-up text");
    await frame();
    assertTrue(Boolean(host.querySelector("[data-queued-id]")), "fixture should show the queued bubble");
    await drainNextQueuedPrompt(sid);
    const queued = userTurn("Queued follow-up text");
    assertTrue(Boolean(queued) && !host.querySelector("[data-queued-id]"), "the queued bubble becomes a user turn");
    assertTrue(!queued!.classList.contains("hoxi-item-enter"), "a dispatched queued prompt must not replay the fade-in");
    assertEqual(getComputedStyle(queued!).opacity, "1", "text already on screen must not blink out");

    setWorkspace("statusBySession", sid, { type: "idle" });
    await dispatchSessionPrompt("Typed and sent directly", sid);
    assertTrue(Boolean(userTurn("Typed and sent directly")?.classList.contains("hoxi-item-enter")),
      "a freshly sent prompt keeps its entrance");
  } finally { dispose(); host.remove(); }
}

export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  const results: string[] = [];
  for (const [name, run] of [
    ["failed sends restore the originating draft and error without touching another session", testInactiveFailure],
    ["a send that fails after unmount is restored on remount", testUnmountFailure],
    ["sending awaits context and keeps reviews bound to the originating session", testReferencedSend],
    ["context failure prevents dispatch and immediate retry succeeds", testReferenceFailure],
    ["failed draft restoration preserves attachments and pasted blocks without duplicates", testDraftAttachments],
    ["a dispatched queued prompt replaces its bubble without blinking", testQueuedDispatchStaysVisible],
  ] as const) { await run(); results.push(name); }
  return results;
}
