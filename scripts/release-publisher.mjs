#!/usr/bin/env node
// Public, dependency-free release API client for the unified version center at
// hoxi.dev. Credentials remain in the environment (HOXI_RELEASE_KEY).
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { normalizeVersion, validatePublication } from "./release-artifact.mjs";
import { httpsURL, validateSparkle } from "./sparkle.mjs";
import { resolveCdnBaseUrl } from "./upload-cdn.mjs";

export const APP_KEY = "hoxi-desktop";
export const DEFAULT_RELEASE_API_URL = "https://hoxi.dev/api/releases";

function githubBase(version) {
  return `https://github.com/hoxiai/hoxi/releases/download/v${version}`;
}

function normalizeCdnVersionBase(cdnBaseUrl, version) {
  if (!cdnBaseUrl) return "";
  const clean = httpsURL(cdnBaseUrl).replace(/\/+$/, "");
  return `${clean}/v${version}`;
}

function assetExtra(assetsMeta, name) {
  const entry = assetsMeta && typeof assetsMeta === "object" ? assetsMeta[name] : null;
  if (!entry || typeof entry !== "object") return {};
  const extra = {};
  if (typeof entry.sha256 === "string" && /^[a-f0-9]{64}$/.test(entry.sha256)) {
    extra.sha256 = entry.sha256;
  }
  if (Number.isSafeInteger(entry.size) && entry.size > 0) {
    extra.size = entry.size;
  }
  return extra;
}

export function releasePayload({
  version,
  notes,
  sha256,
  size,
  mandatory = false,
  sparkle,
  minimumMacOS,
  commit,
  htmlUrl,
  cdnBaseUrl = "",
  assetsMeta,
}) {
  version = normalizeVersion(version);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error("A verified SHA-256 is required");
  if (!Number.isSafeInteger(size) || size <= 0) throw new Error("A non-empty artifact is required");
  const cleanCdnBase = cdnBaseUrl ? httpsURL(cdnBaseUrl).replace(/\/+$/, "") : "";
  const sig = sparkle === undefined
    ? undefined
    : validateSparkle(sparkle, version, size, cleanCdnBase ? { cdnBaseUrl: cleanCdnBase } : {});
  if (minimumMacOS !== undefined && !/^\d+\.\d+(?:\.\d+)?$/.test(minimumMacOS)) {
    throw new Error("Invalid minimum macOS version");
  }

  const ghBase = githubBase(version);
  const cdnVerBase = normalizeCdnVersionBase(cleanCdnBase, version);
  const urlFor = (name) => (cdnVerBase ? `${cdnVerBase}/${name}` : `${ghBase}/${name}`);
  const mirrorsFor = (name) => (cdnVerBase
    ? { cdn: `${cdnVerBase}/${name}`, global: `${ghBase}/${name}` }
    : { global: `${ghBase}/${name}` });

  const macName = `Hoxi-${version}-darwin-arm64.dmg`;
  const macAmd64Name = `Hoxi-${version}-darwin-amd64.dmg`;
  const winName = `Hoxi-${version}-windows-amd64-setup.exe`;
  const winZipName = `Hoxi-${version}-windows-amd64.zip`;
  const winArm64Name = `Hoxi-${version}-windows-arm64-setup.exe`;
  const winArm64ZipName = `Hoxi-${version}-windows-arm64.zip`;
  const linuxName = `Hoxi-${version}-linux-amd64.tar.gz`;

  const macUrl = urlFor(macName);
  const macAmd64Url = urlFor(macAmd64Name);
  const winUrl = urlFor(winName);
  const winArm64Url = urlFor(winArm64Name);
  const linuxUrl = urlFor(linuxName);

  const assets = [
    {
      name: macName, url: macUrl, browser_download_url: macUrl,
      platform: "macos", arch: "arm64", package: "desktop", size, sha256,
      ...(cdnVerBase ? { mirrors: mirrorsFor(macName) } : {}),
      ...(sig ? { edSignature: sig.edSignature, signature: sig.edSignature } : {}),
      ...(minimumMacOS ? { minimumSystemVersion: minimumMacOS } : {}),
    },
    {
      // Intel Mac DMG. Sparkle stays bound to the single arm64 appcast
      // enclosure above; the Intel bundle uses the arch-aware self-updater.
      name: macAmd64Name, url: macAmd64Url, browser_download_url: macAmd64Url,
      platform: "macos", arch: "x64", package: "desktop",
      ...assetExtra(assetsMeta, macAmd64Name),
      ...(cdnVerBase ? { mirrors: mirrorsFor(macAmd64Name) } : {}),
      ...(minimumMacOS ? { minimumSystemVersion: minimumMacOS } : {}),
    },
    {
      name: winName, url: winUrl, browser_download_url: winUrl,
      platform: "windows", arch: "x64", package: "desktop",
      ...assetExtra(assetsMeta, winName),
      ...(cdnVerBase ? { mirrors: mirrorsFor(winName) } : {}),
    },
    {
      name: winArm64Name, url: winArm64Url, browser_download_url: winArm64Url,
      platform: "windows", arch: "arm64", package: "desktop",
      ...assetExtra(assetsMeta, winArm64Name),
      ...(cdnVerBase ? { mirrors: mirrorsFor(winArm64Name) } : {}),
    },
    {
      name: linuxName, url: linuxUrl, browser_download_url: linuxUrl,
      platform: "linux", arch: "x64", package: "desktop",
      ...assetExtra(assetsMeta, linuxName),
      ...(cdnVerBase ? { mirrors: mirrorsFor(linuxName) } : {}),
    },
  ];

  const metaMirrors = cdnVerBase
    ? {
        "mac-arm64": mirrorsFor(macName),
        "mac-x64": mirrorsFor(macAmd64Name),
        "windows-x64": mirrorsFor(winName),
        "windows-amd64": mirrorsFor(winName),
        "windows-x64-zip": mirrorsFor(winZipName),
        "windows-arm64": mirrorsFor(winArm64Name),
        "windows-arm64-zip": mirrorsFor(winArm64ZipName),
        "linux-x64": mirrorsFor(linuxName),
      }
    : undefined;

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
      ...(cleanCdnBase ? { cdnBaseUrl: cleanCdnBase } : {}),
      ...(metaMirrors ? { mirrors: metaMirrors } : {}),
      ...(sig ? { sparkle: sig } : {}),
    },
  };
}

export async function publishRelease(payload, { key, url = DEFAULT_RELEASE_API_URL, fetchImpl = fetch }) {
  if (!key?.trim()) throw new Error("HOXI_RELEASE_KEY is required to publish");
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
  const cdnBaseUrl = input.cdnBaseUrl || resolveCdnBaseUrl(process.env);
  const payload = releasePayload({
    version: input.version,
    notes: input.notes,
    sha256: input.sha256,
    size: input.size ?? input.artifactSize,
    mandatory: input.mandatory,
    sparkle: input.sparkle,
    minimumMacOS: input.minimumMacOS,
    commit: input.commit || process.env.GITHUB_SHA,
    cdnBaseUrl,
    assetsMeta: input.assetsMeta,
  });
  console.log(JSON.stringify(await publishRelease(payload, {
    key: process.env.HOXI_RELEASE_KEY,
    url: process.env.HOXI_RELEASE_API_URL || undefined,
  })));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
