#!/usr/bin/env node
// Public, dependency-free release API client for the unified version center at
// hoxi.dev. Credentials remain in the environment (APAY_RELEASE_KEY, with the
// legacy HOXI_RELEASE_KEY accepted as a fallback).
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { normalizeVersion, validatePublication } from "./release-artifact.mjs";
import { validateSparkle } from "./sparkle.mjs";

export const APP_KEY = "hoxi-desktop";
export const DEFAULT_RELEASE_API_URL = "https://hoxi.dev/api/releases";

function githubBase(version) {
  return `https://github.com/hoxiai/hoxi/releases/download/v${version}`;
}

export function releasePayload({ version, notes, sha256, size, mandatory = false, sparkle, minimumMacOS, commit, htmlUrl }) {
  version = normalizeVersion(version);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error("A verified SHA-256 is required");
  if (!Number.isSafeInteger(size) || size <= 0) throw new Error("A non-empty artifact is required");
  const sig = sparkle === undefined ? undefined : validateSparkle(sparkle, version, size);
  if (minimumMacOS !== undefined && !/^\d+\.\d+(?:\.\d+)?$/.test(minimumMacOS)) {
    throw new Error("Invalid minimum macOS version");
  }

  const base = githubBase(version);
  const macUrl = `${base}/Hoxi-${version}-darwin-arm64.dmg`;
  const winUrl = `${base}/Hoxi-${version}-windows-amd64.zip`;
  const linuxUrl = `${base}/Hoxi-${version}-linux-amd64.tar.gz`;

  const assets = [
    {
      name: `Hoxi-${version}-darwin-arm64.dmg`, url: macUrl, browser_download_url: macUrl,
      platform: "macos", arch: "arm64", package: "desktop", size, sha256,
      ...(sig ? { edSignature: sig.edSignature, signature: sig.edSignature } : {}),
      ...(minimumMacOS ? { minimumSystemVersion: minimumMacOS } : {}),
    },
    {
      name: `Hoxi-${version}-windows-amd64.zip`, url: winUrl, browser_download_url: winUrl,
      platform: "windows", arch: "x64", package: "desktop",
    },
    {
      name: `Hoxi-${version}-linux-amd64.tar.gz`, url: linuxUrl, browser_download_url: linuxUrl,
      platform: "linux", arch: "x64", package: "desktop",
    },
  ];

  const title = `Hoxi Desktop v${version}`;
  const body = notes || title;
  return {
    appKey: APP_KEY,
    appName: "Hoxi Desktop",
    channel: "stable",
    version,
    name: title,
    tagName: `v${version}`,
    ...(commit ? { commit } : {}),
    htmlUrl: htmlUrl || `https://github.com/hoxiai/hoxi/releases/tag/v${version}`,
    mandatory: Boolean(mandatory),
    locales: {
      zh: { title, body },
      en: { title, body },
    },
    assets,
    metaData: {
      ...(minimumMacOS ? { minimumSystemVersion: minimumMacOS } : {}),
      ...(mandatory ? { mandatory: true } : {}),
      ...(sig ? { sparkle: sig } : {}),
    },
  };
}

export async function publishRelease(payload, { key, url = DEFAULT_RELEASE_API_URL, fetchImpl = fetch }) {
  if (!key?.trim()) throw new Error("APAY_RELEASE_KEY (or HOXI_RELEASE_KEY) is required to publish");
  const endpoint = new URL(url);
  const local = ["127.0.0.1", "localhost", "[::1]"].includes(endpoint.hostname);
  if (endpoint.protocol !== "https:" && !(endpoint.protocol === "http:" && local)) {
    throw new Error("Release API must use HTTPS (loopback HTTP is allowed for tests)");
  }
  if (endpoint.username || endpoint.password) throw new Error("Credentials must not be embedded in the release URL");
  const response = await fetchImpl(endpoint, {
    method: "POST", redirect: "error", signal: AbortSignal.timeout(120_000),
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
  if (data?.appKey !== undefined && payload.appKey && data.appKey !== payload.appKey) {
    throw new Error("Release API confirmed a different appKey");
  }
  // The publish acknowledgement alone is not acceptance: the read-back must
  // carry the exact Sparkle metadata, or the macOS feed would be unverifiable.
  if (payload.metaData?.sparkle) {
    const macAsset = (data?.assets || []).find(asset =>
      String(asset.package || "").toLowerCase() === "desktop"
      && ["macos", "darwin", "mac"].includes(String(asset.platform || "").toLowerCase())
      && ["arm64", "aarch64"].includes(String(asset.arch || "").toLowerCase()));
    if (!macAsset || macAsset.edSignature !== payload.metaData.sparkle.edSignature
      || macAsset.size !== payload.metaData.sparkle.length) {
      throw new Error("Website must explicitly persist the exact Sparkle metadata and feed");
    }
  }
  return validatePublication({ success: true, mode: "api", version: payload.version, data });
}

async function main() {
  const input = JSON.parse(fs.readFileSync(0, "utf8"));
  const payload = releasePayload({
    version: input.version,
    notes: input.notes,
    sha256: input.sha256,
    size: input.size ?? input.artifactSize,
    mandatory: input.mandatory,
    sparkle: input.sparkle,
    minimumMacOS: input.minimumMacOS,
    commit: input.commit || process.env.GITHUB_SHA,
  });
  console.log(JSON.stringify(await publishRelease(payload, {
    key: process.env.APAY_RELEASE_KEY || process.env.HOXI_RELEASE_KEY,
    url: process.env.APAY_RELEASE_API_URL || process.env.HOXI_RELEASE_API_URL || undefined,
  })));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
