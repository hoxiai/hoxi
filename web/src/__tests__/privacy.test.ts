import { privacyAPI, PRIVACY_VERSION } from "../services/privacy";
import { clearLocalPromptHistory, dismissPrivacyConsent, ensureAIConsent, loadPrivacy, privacy, PROMPT_HISTORY_CLEARED_EVENT, PROMPT_HISTORY_KEY, savePrivacyConsent, setPrivacy } from "../store/privacy";
import { submitSessionPrompt } from "../store/workspace-session-messages";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

async function withPrivacyFixture(run: () => Promise<void>) {
  const saved = { ...privacy };
  const api = { ...privacyAPI };
  setPrivacy({ loaded: false, accepted: false, saving: false, dialogOpen: false, error: "" });
  try { await run(); }
  finally { Object.assign(privacyAPI, api); setPrivacy(saved); }
}

describe("AI privacy consent", () => {
  it("sends the versioned transport contract without widening consent", async () => {
    const fetch = globalThis.fetch;
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    globalThis.fetch = async (url, init) => {
      calls.push({ url: String(url), init });
      return new Response(JSON.stringify({ version: PRIVACY_VERSION, accepted: false }));
    };
    try {
      await privacyAPI.get();
      await privacyAPI.put(false);
      assertEqual(calls[0].url, "/api/hoxi/privacy");
      assertEqual(calls[1].init?.method, "PUT");
      assertEqual(calls[1].init?.body, JSON.stringify({ version: PRIVACY_VERSION, accepted: false }));
    } finally { globalThis.fetch = fetch; }
  });

  it("clears only prompt recall storage and emits a live reset", async () => {
    const storage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    const windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, "window");
    const values = new Map([[PROMPT_HISTORY_KEY, "old prompts"], ["sessions", "keep"]]);
    const target = new EventTarget();
    let resets = 0;
    target.addEventListener(PROMPT_HISTORY_CLEARED_EVENT, () => resets++);
    Object.defineProperty(globalThis, "window", { configurable: true, value: target });
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
      removeItem: (key: string) => values.delete(key),
    } });
    try {
      assertEqual(clearLocalPromptHistory(), true);
      assertEqual(values.has(PROMPT_HISTORY_KEY), false);
      assertEqual(values.get("sessions"), "keep");
      assertEqual(resets, 1);
    } finally {
      if (storage) Object.defineProperty(globalThis, "localStorage", storage);
      else Reflect.deleteProperty(globalThis, "localStorage");
      if (windowDescriptor) Object.defineProperty(globalThis, "window", windowDescriptor);
      else Reflect.deleteProperty(globalThis, "window");
    }
  });

  it("fails closed before loading and dismissal never grants consent", async () => {
    await withPrivacyFixture(async () => {
      assertEqual(ensureAIConsent(), false);
      assertEqual(privacy.dialogOpen, true);
      dismissPrivacyConsent();
      assertEqual(privacy.accepted, false);
      let rejected = false;
      try { await submitSessionPrompt("must not create a session or send"); }
      catch { rejected = true; }
      assertTrue(rejected);
      assertEqual(privacy.dialogOpen, true);
    });
  });

  it("accepts only the current version and blocks when loading fails", async () => {
    await withPrivacyFixture(async () => {
      privacyAPI.get = async () => ({ version: "old", accepted: true });
      await loadPrivacy();
      assertEqual(ensureAIConsent(), false);
      privacyAPI.get = async () => ({ version: PRIVACY_VERSION, accepted: true });
      await loadPrivacy();
      assertEqual(ensureAIConsent(), true);
      privacyAPI.get = async () => { throw new Error("offline"); };
      await loadPrivacy();
      assertEqual(ensureAIConsent(), false);
      assertTrue(Boolean(privacy.error));
    });
  });

  it("requires a successful explicit opt-in and supports withdrawal", async () => {
    await withPrivacyFixture(async () => {
      privacyAPI.put = async () => { throw new Error("offline"); };
      await savePrivacyConsent(true);
      assertEqual(ensureAIConsent(), false);
      privacyAPI.put = async (accepted) => ({ version: PRIVACY_VERSION, accepted });
      await savePrivacyConsent(true);
      assertEqual(ensureAIConsent(), true);
      await savePrivacyConsent(false);
      assertEqual(ensureAIConsent(), false);
      assertEqual(privacy.accepted, false);
    });
  });

  it("does not let a startup read overwrite a subsequent decline", async () => {
    await withPrivacyFixture(async () => {
      let resolve!: (value: { version: string; accepted: boolean }) => void;
      privacyAPI.get = () => new Promise((done) => { resolve = done; });
      privacyAPI.put = async (accepted) => ({ version: PRIVACY_VERSION, accepted });
      const read = loadPrivacy();
      const save = savePrivacyConsent(false);
      resolve({ version: PRIVACY_VERSION, accepted: true });
      await read;
      await save;
      assertEqual(ensureAIConsent(), false);
    });
  });
});
