#!/usr/bin/env node
// Public, dependency-free release API client. Credentials remain in the environment.
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { normalizeVersion, validatePublication } from "./release-artifact.mjs";
import { validateSparkle } from "./sparkle.mjs";

export function releasePayload({ version, notes, sha256, size, mandatory = false, sparkle, minimumMacOS, gitcode = false }) {
  version = normalizeVersion(version);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error("A verified SHA-256 is required");
  if (!Number.isSafeInteger(size) || size <= 0) throw new Error("A non-empty artifact is required");
  if (sparkle !== undefined) sparkle = validateSparkle(sparkle, version, size);
  if (minimumMacOS !== undefined && !/^\d+\.\d+(?:\.\d+)?$/.test(minimumMacOS)) {
    throw new Error("Invalid minimum macOS version");
  }
  const asset = `Hoxi-${version}-darwin-arm64.dmg`;
  const cn = `https://gitcode.com/hoxiai/hoxi-releases/releases/download/v${version}/${asset}`;
  const global = `https://github.com/hoxiai/hoxi/releases/download/v${version}/${asset}`;
  return {
    version, title: `Hoxi Desktop v${version}`, notes, sha256, size, mandatory,
    mirrors: { "mac-arm64": gitcode ? { cn, global } : { global } },
    desktop: { "mac-arm64": global }, dryRun: false,
    ...(sparkle ? { sparkle: validateSparkle(sparkle, version, size) } : {}),
    ...(minimumMacOS ? { minimumMacOS } : {}),
  };
}

export async function publishRelease(payload, { key, url = "https://hoxi.ai/api/hoxi/releases/publish", fetchImpl = fetch }) {
  if (!key?.trim()) throw new Error("HOXI_RELEASE_KEY is required to publish");
  const endpoint = new URL(url);
  const local = ["127.0.0.1", "localhost", "[::1]"].includes(endpoint.hostname);
  if (endpoint.protocol !== "https:" && !(endpoint.protocol === "http:" && local)) {
    throw new Error("Release API must use HTTPS (loopback HTTP is allowed for tests)");
  }
  if (endpoint.username || endpoint.password) throw new Error("Credentials must not be embedded in the release URL");
  const response = await fetchImpl(endpoint, {
    method: "POST", redirect: "error", signal: AbortSignal.timeout(payload.sparkle ? 120_000 : 30_000),
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}`, "x-release-key": key },
    body: JSON.stringify(payload),
  });
  if (!response.ok) throw new Error(`Release API returned HTTP ${response.status}`);
  const result = await response.json();
  const data = result?.data ?? result;
  const confirmed = result?.success === true || (result?.success !== false && result?.code === 0);
  if (!confirmed || result?.dryRun === true || data?.dryRun === true ||
      result?.publication === "skipped" || data?.publication === "skipped") {
    throw new Error("Release API did not confirm a publication");
  }
  if (data?.version !== undefined && data.version !== payload.version) {
    throw new Error("Release API confirmed a different version");
  }
  if (payload.sparkle && (data.version !== payload.version || data.sparkleAccepted !== true ||
      JSON.stringify(validateSparkle(data.sparkle, payload.version, payload.size)) !== JSON.stringify(payload.sparkle))) {
    throw new Error("Website must explicitly confirm persisted Sparkle metadata and feed deployment");
  }
  return validatePublication({ success: true, mode: "api", version: payload.version, data });
}

async function main() {
  const payload = releasePayload(JSON.parse(fs.readFileSync(0, "utf8")));
  console.log(JSON.stringify(await publishRelease(payload, {
    key: process.env.HOXI_RELEASE_KEY,
    url: process.env.HOXI_RELEASE_API_URL || undefined,
  })));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
