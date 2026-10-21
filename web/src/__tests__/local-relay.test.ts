import { relayAPI, type RelayKeyInfo } from "../services/relay";
import {
  createLocalRelayKey,
  dismissCreatedRelayToken,
  loadLocalRelay,
  localRelay,
  revokeLocalRelayKey,
  setLocalRelay,
} from "../store/relay";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setLocalRelay({
    baseURL: "",
    chatURL: "",
    modelsURL: "",
    keys: [],
    loaded: false,
    busy: false,
    error: "",
    createdToken: "",
  });
}

const sampleKey: RelayKeyInfo = { id: "rk_abc", prefix: "hk-1234567…", label: "Cline", createdAt: 1 };

describe("local model relay", () => {
  it("loads the endpoint and key metadata", async () => {
    const api = { ...relayAPI };
    try {
      relayAPI.getStatus = async () => ({
        ok: true,
        baseURL: "http://127.0.0.1:5410",
        endpoints: {
          chatCompletions: "http://127.0.0.1:5410/v1/chat/completions",
          models: "http://127.0.0.1:5410/v1/models",
        },
        keys: [sampleKey],
      });
      await loadLocalRelay();
      assertEqual(localRelay.baseURL, "http://127.0.0.1:5410");
      assertEqual(localRelay.chatURL, "http://127.0.0.1:5410/v1/chat/completions");
      assertEqual(localRelay.keys.length, 1);
      assertEqual(localRelay.keys[0].prefix, sampleKey.prefix);
      assertEqual(localRelay.loaded, true);
    } finally {
      Object.assign(relayAPI, api);
      reset();
    }
  });

  it("keeps the issued token only in memory and clears it on dismiss", async () => {
    const api = { ...relayAPI };
    try {
      relayAPI.createKey = async (label) => ({ key: { ...sampleKey, label }, token: "hk-plaintext-once" });
      const ok = await createLocalRelayKey("Cline");
      assertEqual(ok, true);
      assertEqual(localRelay.createdToken, "hk-plaintext-once");
      assertEqual(localRelay.keys.length, 1);
      assertEqual(localRelay.keys[0].label, "Cline");
      dismissCreatedRelayToken();
      assertEqual(localRelay.createdToken, "");
    } finally {
      Object.assign(relayAPI, api);
      reset();
    }
  });

  it("surfaces gateway error text and does not add a key on failure", async () => {
    const api = { ...relayAPI };
    try {
      relayAPI.createKey = async () => {
        throw new Error('HTTP 500: {"error":"relay key store is unavailable"}');
      };
      const ok = await createLocalRelayKey("");
      assertEqual(ok, false);
      assertEqual(localRelay.keys.length, 0);
      assertEqual(localRelay.createdToken, "");
      assertEqual(localRelay.error, "relay key store is unavailable");
      assertEqual(localRelay.busy, false);
    } finally {
      Object.assign(relayAPI, api);
      reset();
    }
  });

  it("replaces the key list after a revoke", async () => {
    const api = { ...relayAPI };
    try {
      setLocalRelay({ keys: [sampleKey, { ...sampleKey, id: "rk_other" }] });
      relayAPI.revokeKey = async () => ({ ok: true, keys: [] });
      const ok = await revokeLocalRelayKey("rk_abc");
      assertEqual(ok, true);
      assertEqual(localRelay.keys.length, 0);
      assertEqual(localRelay.error, "");
    } finally {
      Object.assign(relayAPI, api);
      reset();
    }
  });

  it("ignores a second create while one is in flight", async () => {
    const api = { ...relayAPI };
    try {
      let calls = 0;
      relayAPI.createKey = async () => {
        calls++;
        return { key: sampleKey, token: "hk-once" };
      };
      setLocalRelay({ busy: true });
      const ok = await createLocalRelayKey("");
      assertEqual(ok, false);
      assertEqual(calls, 0);
      assertTrue(localRelay.busy);
    } finally {
      Object.assign(relayAPI, api);
      reset();
    }
  });
});
