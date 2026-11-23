#!/usr/bin/env node
// Dependency-free S3-compatible uploader (Cloudflare R2 / OSS / COS) using AWS
// Signature V4 so Hoxi Desktop releases mirror onto cdn.hoxi.dev (or any future
// official download domain) alongside GitHub Releases.
//
// Environment variables (never commit credentials):
//   HOXI_CDN_ENDPOINT           S3 endpoint, e.g. https://<account>.r2.cloudflarestorage.com
//   HOXI_CDN_BUCKET             Bucket name, e.g. hoxi
//   HOXI_CDN_ACCESS_KEY_ID      Access key ID (or Cloudflare API token ID)
//   HOXI_CDN_SECRET_ACCESS_KEY  Secret access key; falls back to SHA-256(HOXI_CDN_TOKEN)
//   HOXI_CDN_TOKEN              Cloudflare API token alternative for secret key derivation
//   HOXI_CDN_PUBLIC_URL         Public HTTPS download origin, e.g. https://cdn.hoxi.dev
//   HOXI_CDN_REGION             Optional S3 signing region (default: auto)
//   HOXI_CDN_PREFIX             Optional key prefix (default: hoxi-desktop)
//   HOXI_CDN_ADDRESSING_STYLE   Optional: path (default for R2) or virtual (for OSS/COS;
//                               auto-detected when endpoint host starts with "<bucket>.")
import { createHash, createHmac } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { normalizeVersion } from "./release-artifact.mjs";

export const DEFAULT_CDN_PREFIX = "hoxi-desktop";
export const IMMUTABLE_CACHE_CONTROL = "public, max-age=31536000, immutable";

const sha256hex = (data) => createHash("sha256").update(data).digest("hex");
const hmac = (key, data) => createHmac("sha256", key).update(data).digest();

export const encodePath = (value) =>
  String(value)
    .split("/")
    .filter((segment) => segment.length > 0)
    .map(encodeURIComponent)
    .join("/");

function normalizeHttpsOrigin(raw, label) {
  const value = String(raw || "").trim();
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${label} must be a valid HTTPS URL`);
  }
  if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.hash || parsed.search) {
    throw new Error(`${label} must be a clean HTTPS URL without credentials, query or fragment`);
  }
  return value.replace(/\/+$/, "");
}

function normalizePrefix(raw) {
  const cleaned = String(raw || DEFAULT_CDN_PREFIX)
    .trim()
    .replace(/^\/+|\/+$/g, "");
  if (!cleaned || cleaned.includes("..")) {
    throw new Error(`Invalid HOXI_CDN_PREFIX: ${JSON.stringify(raw)}`);
  }
  return cleaned;
}

export function resolveCdnBaseUrl(env = process.env) {
  const pub = String(env.HOXI_CDN_PUBLIC_URL || "").trim();
  if (!pub) return "";
  const origin = normalizeHttpsOrigin(pub, "HOXI_CDN_PUBLIC_URL");
  const prefix = normalizePrefix(env.HOXI_CDN_PREFIX);
  return `${origin}/${encodePath(prefix)}`;
}

export function hasCdnConfig(env = process.env) {
  const raw = {
    endpoint: String(env.HOXI_CDN_ENDPOINT || "").trim(),
    bucket: String(env.HOXI_CDN_BUCKET || "").trim(),
    accessKeyId: String(env.HOXI_CDN_ACCESS_KEY_ID || "").trim(),
    secret: String(env.HOXI_CDN_SECRET_ACCESS_KEY || "").trim() || String(env.HOXI_CDN_TOKEN || "").trim(),
    publicUrl: String(env.HOXI_CDN_PUBLIC_URL || "").trim(),
  };
  const present = Object.values(raw).filter(Boolean).length;
  if (present === 0) return false;
  resolveCdnConfig(env);
  return true;
}

export function resolveCdnConfig(env = process.env) {
  const endpoint = String(env.HOXI_CDN_ENDPOINT || "").trim();
  const bucket = String(env.HOXI_CDN_BUCKET || "").trim();
  const accessKeyId = String(env.HOXI_CDN_ACCESS_KEY_ID || "").trim();
  const secretAccessKey =
    String(env.HOXI_CDN_SECRET_ACCESS_KEY || "").trim() ||
    (String(env.HOXI_CDN_TOKEN || "").trim() ? sha256hex(String(env.HOXI_CDN_TOKEN).trim()) : "");
  const publicUrl = String(env.HOXI_CDN_PUBLIC_URL || "").trim();

  const missing = [];
  if (!endpoint) missing.push("HOXI_CDN_ENDPOINT");
  if (!bucket) missing.push("HOXI_CDN_BUCKET");
  if (!accessKeyId) missing.push("HOXI_CDN_ACCESS_KEY_ID");
  if (!secretAccessKey) missing.push("HOXI_CDN_SECRET_ACCESS_KEY (or HOXI_CDN_TOKEN)");
  if (!publicUrl) missing.push("HOXI_CDN_PUBLIC_URL");
  if (missing.length > 0) {
    throw new Error(`Incomplete CDN storage configuration; missing: ${missing.join(", ")}`);
  }

  const rawStyle = String(env.HOXI_CDN_ADDRESSING_STYLE || "").trim().toLowerCase();
  if (rawStyle && rawStyle !== "path" && rawStyle !== "virtual") {
    throw new Error(`Invalid HOXI_CDN_ADDRESSING_STYLE: ${JSON.stringify(env.HOXI_CDN_ADDRESSING_STYLE)} (expected path or virtual)`);
  }
  const normalizedEndpoint = normalizeHttpsOrigin(endpoint, "HOXI_CDN_ENDPOINT");
  const endpointHost = new URL(normalizedEndpoint).hostname.toLowerCase();
  const addressingStyle = rawStyle || (endpointHost.startsWith(`${bucket.toLowerCase()}.`) ? "virtual" : "path");

  return {
    endpoint: normalizedEndpoint,
    bucket,
    accessKeyId,
    secretAccessKey,
    publicUrl: normalizeHttpsOrigin(publicUrl, "HOXI_CDN_PUBLIC_URL"),
    region: String(env.HOXI_CDN_REGION || "auto").trim() || "auto",
    prefix: normalizePrefix(env.HOXI_CDN_PREFIX),
    addressingStyle,
  };
}

export function signedRequest({
  method,
  endpoint,
  bucket,
  key,
  region,
  accessKeyId,
  secretAccessKey,
  addressingStyle = "path",
  body = Buffer.alloc(0),
  headers = {},
  now = new Date(),
}) {
  const base = new URL(endpoint);
  const useVirtual = addressingStyle === "virtual" || base.hostname.toLowerCase().startsWith(`${String(bucket).toLowerCase()}.`);
  const host = useVirtual && !base.hostname.toLowerCase().startsWith(`${String(bucket).toLowerCase()}.`)
    ? `${bucket}.${base.host}`
    : base.host;
  const origin = `${base.protocol}//${host}`;
  const reqPath = useVirtual
    ? `/${encodePath(key)}`
    : `/${encodePath(bucket)}/${encodePath(key)}`;
  const timestamp = now.toISOString().replace(/[:-]|\.\d{3}/g, "");
  const date = timestamp.slice(0, 8);
  const payloadHash = sha256hex(body);

  const normalizedHeaders = {};
  for (const [k, v] of Object.entries(headers)) {
    if (v !== undefined && v !== null) {
      normalizedHeaders[k.toLowerCase()] = String(v).trim();
    }
  }
  const signed = {
    host,
    "x-amz-content-sha256": payloadHash,
    "x-amz-date": timestamp,
  };
  for (const [k, v] of Object.entries(normalizedHeaders)) {
    if (k.startsWith("x-amz-meta-")) {
      signed[k] = v;
    }
  }
  const names = Object.keys(signed).sort();
  const canonical = [
    method,
    reqPath,
    "",
    ...names.map((name) => `${name}:${signed[name]}`),
    "",
    names.join(";"),
    payloadHash,
  ].join("\n");
  const scope = `${date}/${region}/s3/aws4_request`;
  const toSign = ["AWS4-HMAC-SHA256", timestamp, scope, sha256hex(canonical)].join("\n");
  const signingKey = ["aws4_request"].reduce(
    hmac,
    hmac(hmac(hmac(`AWS4${secretAccessKey}`, date), region), "s3")
  );
  const signature = createHmac("sha256", signingKey).update(toSign).digest("hex");

  return {
    url: `${origin}${reqPath}`,
    init: {
      method,
      headers: {
        ...normalizedHeaders,
        "x-amz-content-sha256": payloadHash,
        "x-amz-date": timestamp,
        authorization: `AWS4-HMAC-SHA256 Credential=${accessKeyId}/${scope}, SignedHeaders=${names.join(";")}, Signature=${signature}`,
      },
      ...(method === "PUT" ? { body } : {}),
    },
  };
}

async function fetchWithRetry(fetchImpl, makeRequest, maxAttempts = 3) {
  let lastError;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      const req = makeRequest();
      const res = await fetchImpl(req.url, { ...req.init, signal: AbortSignal.timeout(180_000) });
      if (res.status >= 500 && attempt < maxAttempts) {
        await res.body?.cancel?.();
        await new Promise((r) => setTimeout(r, attempt * 1000));
        continue;
      }
      return res;
    } catch (err) {
      lastError = err;
      if (attempt < maxAttempts) {
        await new Promise((r) => setTimeout(r, attempt * 1000));
        continue;
      }
    }
  }
  throw lastError;
}

export async function uploadToCdn({
  config,
  file,
  key,
  contentType = "application/octet-stream",
  cacheControl = IMMUTABLE_CACHE_CONTROL,
  allowOverwrite = false,
  fetchImpl = fetch,
}) {
  const cleanKey = String(key).replace(/^\/+/, "");
  const bytes = fs.readFileSync(file);
  const size = bytes.length;
  const digest = sha256hex(bytes);
  const publicUrl = `${config.publicUrl.replace(/\/+$/, "")}/${encodePath(cleanKey)}`;

  if (!allowOverwrite) {
    const head = await fetchWithRetry(fetchImpl, () =>
      signedRequest({ method: "HEAD", ...config, key: cleanKey })
    );
    if (head.ok) {
      const remoteSha = String(head.headers?.get?.("x-amz-meta-sha256") || "").trim().toLowerCase();
      const remoteLen = Number(head.headers?.get?.("content-length") || 0);
      if (remoteSha && remoteSha === digest && (!remoteLen || remoteLen === size)) {
        return { url: publicUrl, key: cleanKey, size, sha256: digest, skipped: true };
      }
      const existing = await fetchWithRetry(fetchImpl, () =>
        signedRequest({ method: "GET", ...config, key: cleanKey })
      );
      if (!existing.ok) {
        throw new Error(`CDN verification GET ${cleanKey} returned HTTP ${existing.status}`);
      }
      const existingDigest = sha256hex(Buffer.from(await existing.arrayBuffer()));
      if (existingDigest === digest) {
        return { url: publicUrl, key: cleanKey, size, sha256: digest, skipped: true };
      }
      throw new Error(
        `CDN already contains ${cleanKey} with different bytes; bump version or pass --allow-overwrite`
      );
    } else if (head.status !== 404) {
      throw new Error(`CDN HEAD ${cleanKey} returned HTTP ${head.status}`);
    }
  }

  const headers = {
    "content-type": contentType,
    "cache-control": cacheControl,
    "x-amz-meta-sha256": digest,
  };
  if (!allowOverwrite) {
    headers["if-none-match"] = "*";
  }

  const stored = await fetchWithRetry(fetchImpl, () =>
    signedRequest({ method: "PUT", ...config, key: cleanKey, body: bytes, headers })
  );
  if (stored.status === 412) {
    throw new Error(`CDN object ${cleanKey} appeared concurrently; re-run to verify digest`);
  }
  if (!stored.ok) {
    throw new Error(`CDN upload ${cleanKey} failed with HTTP ${stored.status}`);
  }
  return { url: publicUrl, key: cleanKey, size, sha256: digest, skipped: false };
}

export function desktopReleaseFiles(version) {
  const ver = normalizeVersion(version);
  return [
    { name: `Hoxi-${ver}-darwin-arm64.dmg`, contentType: "application/x-apple-diskimage" },
    { name: `Hoxi-${ver}-darwin-arm64.dmg.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: `Hoxi-${ver}-darwin-amd64.dmg`, contentType: "application/x-apple-diskimage" },
    { name: `Hoxi-${ver}-darwin-amd64.dmg.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: "checksums.txt", contentType: "text/plain; charset=utf-8" },
    { name: "sparkle.json", contentType: "application/json; charset=utf-8" },
    { name: `Hoxi-${ver}-windows-amd64-setup.exe`, contentType: "application/vnd.microsoft.portable-executable" },
    { name: `Hoxi-${ver}-windows-amd64-setup.exe.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: `Hoxi-${ver}-windows-amd64.zip`, contentType: "application/zip" },
    { name: `Hoxi-${ver}-windows-amd64.zip.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: `Hoxi-${ver}-windows-arm64-setup.exe`, contentType: "application/vnd.microsoft.portable-executable" },
    { name: `Hoxi-${ver}-windows-arm64-setup.exe.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: `Hoxi-${ver}-windows-arm64.zip`, contentType: "application/zip" },
    { name: `Hoxi-${ver}-windows-arm64.zip.sha256`, contentType: "text/plain; charset=utf-8" },
    { name: `Hoxi-${ver}-linux-amd64.tar.gz`, contentType: "application/gzip" },
    { name: `Hoxi-${ver}-linux-amd64.tar.gz.sha256`, contentType: "text/plain; charset=utf-8" },
  ];
}

export async function syncDesktopRelease({
  version,
  dir,
  config = resolveCdnConfig(),
  allowOverwrite = false,
  fetchImpl = fetch,
}) {
  const ver = normalizeVersion(version);
  const files = desktopReleaseFiles(ver);
  for (const item of files) {
    const fullPath = path.join(dir, item.name);
    if (!fs.existsSync(fullPath)) {
      throw new Error(`Missing release artifact for CDN sync: ${fullPath}`);
    }
  }
  const uploaded = [];
  for (const item of files) {
    const fullPath = path.join(dir, item.name);
    const key = `${config.prefix}/v${ver}/${item.name}`;
    const res = await uploadToCdn({
      config,
      file: fullPath,
      key,
      contentType: item.contentType,
      allowOverwrite,
      fetchImpl,
    });
    uploaded.push({ name: item.name, ...res });
  }
  return {
    version: ver,
    cdnBaseUrl: `${config.publicUrl.replace(/\/+$/, "")}/${encodePath(config.prefix)}`,
    uploaded: uploaded.filter((f) => !f.skipped).length,
    skipped: uploaded.filter((f) => f.skipped).length,
    files: uploaded,
  };
}

function parseFlags(argv) {
  const opts = {
    file: "",
    key: "",
    dir: "",
    version: "",
    contentType: "application/octet-stream",
    allowOverwrite: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--file") opts.file = argv[++i];
    else if (arg === "--key") opts.key = argv[++i];
    else if (arg === "--dir") opts.dir = argv[++i];
    else if (arg === "--version") opts.version = argv[++i];
    else if (arg === "--content-type") opts.contentType = argv[++i];
    else if (arg === "--allow-overwrite") opts.allowOverwrite = true;
    else throw new Error(`Unknown option: ${arg}`);
  }
  return opts;
}

async function main() {
  const [command, ...rest] = process.argv.slice(2);
  if (command === "check") {
    const enabled = hasCdnConfig(process.env);
    console.log(JSON.stringify({ enabled, cdnBaseUrl: enabled ? resolveCdnBaseUrl(process.env) : "" }));
    return;
  }
  if (command === "sync-release") {
    const opts = parseFlags(rest);
    if (!opts.version) throw new Error("--version is required");
    if (!opts.dir) throw new Error("--dir is required");
    const result = await syncDesktopRelease({
      version: opts.version,
      dir: opts.dir,
      config: resolveCdnConfig(process.env),
      allowOverwrite: opts.allowOverwrite,
    });
    console.log(JSON.stringify(result));
    return;
  }
  const opts = parseFlags(process.argv.slice(2));
  if (!opts.file) throw new Error("--file is required");
  if (!opts.key) throw new Error("--key is required");
  const result = await uploadToCdn({
    config: resolveCdnConfig(process.env),
    file: opts.file,
    key: opts.key,
    contentType: opts.contentType,
    allowOverwrite: opts.allowOverwrite,
  });
  console.log(JSON.stringify(result));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(`upload-cdn: ${error.message}`);
    process.exitCode = 1;
  });
}
