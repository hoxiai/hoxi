import { cloudflaredAPI, type CloudflaredStatus } from "../services/cloudflared";
import {
  cloudflared,
  disableCloudflared,
  enableCloudflared,
  loadCloudflaredStatus,
  setCloudflared,
} from "../store/cloudflared";
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
});