import { cloudflaredAPI, type CloudflaredStatus } from "../services/cloudflared";
import { pairingAPI } from "../services/pairing";
import { remoteTunnelAPI, type RemoteTunnelStatus } from "../services/remote-tunnel";
import {
  cloudflared,
  disableCloudflared,
  enableCloudflared,
  loadCloudflaredStatus,
  setCloudflared,
} from "../store/cloudflared";
import {
  cancelRemoteTunnel,
  loadRemoteTunnelStatus,
  remoteTunnel,
  requestRemoteTunnel,
  setRemoteTunnel,
} from "../store/remote-tunnel";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setCloudflared({ status: { enabled: false, state: "disabled", hasToken: false }, loaded: false, busy: false, error: "" });
}

const onlineStatus: CloudflaredStatus = {
  enabled: true,
  state: "online",
  hostname: "abc123.node.hoxi.work",
  binary: "/usr/local/bin/cloudflared",
  hasToken: true,
  ingressUrl: "http://127.0.0.1:5418",
};

describe("cloudflare tunnel connector", () => {
  it("loads the connector status", async () => {
    const api = { ...cloudflaredAPI };
    try {
      cloudflaredAPI.getStatus = async () => onlineStatus;
      await loadCloudflaredStatus();
      assertEqual(cloudflared.loaded, true);
      assertEqual(cloudflared.status.state, "online");
      assertEqual(cloudflared.status.hasToken, true);
      assertEqual(cloudflared.status.ingressUrl, "http://127.0.0.1:5418");
    } finally {
      Object.assign(cloudflaredAPI, api);
      reset();
    }
  });

  it("enables the connector and never stores the token in the status", async () => {
    const api = { ...cloudflaredAPI };
    try {
      let sentToken = "";
      cloudflaredAPI.enable = async (token, hostname) => {
        sentToken = token;
        return { ...onlineStatus, hostname };
      };
      const ok = await enableCloudflared("cf-connector-token", "abc123.node.hoxi.work");
      assertEqual(ok, true);
      assertEqual(sentToken, "cf-connector-token");
      assertEqual(cloudflared.status.enabled, true);
      assertTrue(
        !JSON.stringify(cloudflared.status).includes("cf-connector-token"),
        "status must not echo the connector token",
      );
    } finally {
      Object.assign(cloudflaredAPI, api);
      reset();
    }
  });

  it("disables the connector", async () => {
    const api = { ...cloudflaredAPI };
    try {
      setCloudflared("status", onlineStatus);
      cloudflaredAPI.disable = async () => ({ enabled: false, state: "disabled", hasToken: true });
      await disableCloudflared();
      assertEqual(cloudflared.status.enabled, false);
      assertEqual(cloudflared.status.state, "disabled");
    } finally {
      Object.assign(cloudflaredAPI, api);
      reset();
    }
  });

  it("surfaces an enable error", async () => {
    const api = { ...cloudflaredAPI };
    try {
      cloudflaredAPI.enable = async () => {
        throw new Error("HTTP 400: boom");
      };
      const ok = await enableCloudflared("x", "");
      assertEqual(ok, false);
      assertTrue(cloudflared.error.length > 0, "error should be set");
    } finally {
      Object.assign(cloudflaredAPI, api);
      reset();
    }
  });

  it("ignores stale status polls across enable/disable and refreshes LAN pairing QR", async () => {
    const cfApi = { ...cloudflaredAPI };
    const pairApi = { ...pairingAPI };
    let resolveStalePoll!: (s: CloudflaredStatus) => void;
    let pairingRefreshCount = 0;
    try {
      pairingAPI.getCode = async () => {
        pairingRefreshCount++;
        return {
          url: "http://192.168.1.20:5417",
          token: "a".repeat(64),
          type: "hoxi",
          entrance: "",
          host: "192.168.1.20",
          port: 5417,
          hostname: "Studio",
          relay: "https://abc123.node.hoxi.work",
          available: true,
          qrPng: "data:image/png;base64,AAAA",
        };
      };
      cloudflaredAPI.getStatus = () => new Promise<CloudflaredStatus>((r) => { resolveStalePoll = r; });
      cloudflaredAPI.enable = async (_token, hostname) => ({ ...onlineStatus, hostname });

      const stalePoll = loadCloudflaredStatus();
      const ok = await enableCloudflared("tok", "abc123.node.hoxi.work");
      assertEqual(ok, true);
      assertEqual(cloudflared.status.enabled, true);
      assertTrue(pairingRefreshCount >= 1, "enabling cloudflared must refresh pairing QR");

      resolveStalePoll({ enabled: false, state: "disabled", hasToken: false });
      await stalePoll;
      assertEqual(cloudflared.status.enabled, true, "stale poll must not overwrite post-enable status");
    } finally {
      Object.assign(cloudflaredAPI, cfApi);
      Object.assign(pairingAPI, pairApi);
      reset();
    }
  });

  it("guards remoteTunnel mutations against stale polls and concurrent cancel", async () => {
    const rtApi = { ...remoteTunnelAPI };
    let resolveStalePoll!: (s: RemoteTunnelStatus) => void;
    try {
      remoteTunnelAPI.getStatus = () => new Promise<RemoteTunnelStatus>((r) => { resolveStalePoll = r; });
      remoteTunnelAPI.requestPair = async () => ({
        enabled: false,
        state: "connecting",
        controllerUrl: "https://panel.example.com",
        pending: { requestId: "r1", userCode: "ABCD-1234", expiresAt: "2099-01-01T00:00:00Z", state: "waiting" },
      });

      const stalePoll = loadRemoteTunnelStatus();
      const ok = await requestRemoteTunnel({ controllerUrl: "https://panel.example.com", name: "Mac" });
      assertEqual(ok, true);
      assertEqual(remoteTunnel.status.pending?.state, "waiting");

      resolveStalePoll({ enabled: false, state: "disabled" });
      await stalePoll;
      assertEqual(remoteTunnel.status.pending?.state, "waiting", "stale poll must not overwrite pending pair");

      let cancelCalls = 0;
      remoteTunnelAPI.cancelPair = async () => {
        cancelCalls++;
        return { enabled: false, state: "disabled" };
      };
      setRemoteTunnel("busy", true);
      await cancelRemoteTunnel();
      assertEqual(cancelCalls, 0, "cancelRemoteTunnel must be guarded while busy");
      setRemoteTunnel("busy", false);
      await cancelRemoteTunnel();
      assertEqual(cancelCalls, 1);
      assertEqual(remoteTunnel.status.state, "disabled");
    } finally {
      Object.assign(remoteTunnelAPI, rtApi);
      setRemoteTunnel({ status: { enabled: false, state: "disabled" }, loaded: false, busy: false, error: "" });
    }
  });
});