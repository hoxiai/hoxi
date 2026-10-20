#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { normalizeVersion } from "./release-artifact.mjs";

const API_BASE = process.env.GITCODE_API_BASE || "https://api.gitcode.com/api/v5";
const DEFAULT_REPO = "hoxiai/hoxi-releases";

export class GitcodeError extends Error {
  constructor(message, status, body) {
    super(message);
    this.name = "GitcodeError";
    this.status = status;
    this.body = body;
  }
}

function validateRepo(repo) {
  if (!/^[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+$/.test(repo)) {
    throw new Error("GitCode repository must use owner/repo format");
  }
  return repo;
}

function releaseUrl(repo, tag) {
  return `${API_BASE}/repos/${repo}/releases/tags/${encodeURIComponent(tag)}`;
}

function uploadUrl(repo, tag, name) {
  return `${API_BASE}/repos/${repo}/releases/${encodeURIComponent(tag)}/upload_url?file_name=${encodeURIComponent(name)}`;
}

function describeBody(text) {
  return text.replace(/\s+/g, " ").trim().slice(0, 300);
}

export async function gitcodeRequest({ token, method, url, body, fetchImpl = fetch, timeout = 30_000 }) {
  if (!token?.trim()) throw new Error("GITCODE_TOKEN is required");
  const target = new URL(url);
  if (target.protocol !== "https:") throw new Error("GitCode API requires HTTPS");
  const headers = { "PRIVATE-TOKEN": token, Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetchImpl(target, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: "error",
    signal: AbortSignal.timeout(timeout),
  });
  const text = await response.text();
  if (!response.ok) {
    if (response.status === 401) {
      throw new GitcodeError("GitCode token is invalid or expired (HTTP 401)", response.status);
    }
    const detail = describeBody(text);
    throw new GitcodeError(`${method} ${target.pathname} failed: HTTP ${response.status}${detail ? ` (${detail})` : ""}`,
      response.status);
  }
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new GitcodeError(`${method} ${target.pathname} returned invalid JSON`, response.status);
  }
}

async function getRelease(ctx) {
  try {
    return await gitcodeRequest({ ...ctx, method: "GET", url: releaseUrl(ctx.repo, ctx.tag) });
  } catch (error) {
    if (error instanceof GitcodeError && error.status === 404) return null;
    throw error;
  }
}

async function createRelease(ctx, notes, target) {
  await gitcodeRequest({
    ...ctx,
    method: "POST",
    url: `${API_BASE}/repos/${ctx.repo}/releases`,
    body: {
      tag_name: ctx.tag,
      name: `Hoxi Desktop ${ctx.tag}`,
      body: notes,
      target_commitish: target,
      release_status: "pre",
    },
  });
}

async function setReleaseState(ctx, notes, status) {
  await gitcodeRequest({
    ...ctx,
    method: "PATCH",
    url: `${API_BASE}/repos/${ctx.repo}/releases/${encodeURIComponent(ctx.tag)}`,
    body: {
      name: `Hoxi Desktop ${ctx.tag}`,
      body: notes,
      release_status: status,
    },
  });
}

function attachedAssets(release) {
  return Array.isArray(release?.assets)
    ? release.assets.filter((asset) => (asset.type ?? "attach") === "attach")
    : [];
}

async function deleteAttachedAsset(ctx, asset) {
  const id = asset.id ?? asset.attach_file_id;
  if (id === undefined || id === null) {
    throw new Error(`GitCode asset ${asset.name} has no attachment id`);
  }
  await gitcodeRequest({
    ...ctx,
    method: "DELETE",
    url: `${API_BASE}/repos/${ctx.repo}/releases/${encodeURIComponent(ctx.tag)}/attach_files/${encodeURIComponent(id)}`,
  });
}

async function fileBody(file) {
  if (typeof fs.openAsBlob === "function") return fs.openAsBlob(file);
  return fs.readFileSync(file);
}

async function uploadAsset(ctx, file) {
  const name = path.basename(file);
  const upload = await gitcodeRequest({
    ...ctx,
    method: "GET",
    url: uploadUrl(ctx.repo, ctx.tag, name),
  });
  if (!upload?.url) throw new Error(`GitCode did not return an upload URL for ${name}`);
  const target = new URL(upload.url);
  if (target.protocol !== "https:") throw new Error(`GitCode upload URL for ${name} is not HTTPS`);
  const headers = {};
  for (const [key, value] of Object.entries(upload.headers || {})) {
    const text = String(value);
    if (!/^[A-Za-z0-9-]+$/.test(key) || /[\r\n]/.test(text)) {
      throw new Error(`GitCode returned an invalid upload header for ${name}`);
    }
    headers[key] = text;
  }
  const body = await fileBody(file);
  const response = await ctx.fetchImpl(target, {
    method: "PUT",
    headers,
    body,
    redirect: "error",
    signal: AbortSignal.timeout(30 * 60_000),
  });
  const text = await response.text();
  if (!response.ok) {
    throw new GitcodeError(`PUT upload for ${name} failed: HTTP ${response.status}${text ? ` (${describeBody(text)})` : ""}`,
      response.status);
  }
}

function verifyRelease(release, expected, status) {
  if (!release || release.tag_name !== expected.tag) throw new Error("GitCode release tag does not match");
  if (release.release_status !== status) throw new Error(`GitCode release status is not ${status}`);
  const assets = attachedAssets(release);
  const names = assets.map((asset) => asset.name).sort();
  const wanted = expected.names.slice().sort();
  if (names.length !== wanted.length || names.some((name, index) => name !== wanted[index])) {
    throw new Error(`GitCode asset set differs from the published set: ${names.join(", ")}`);
  }
  for (const asset of assets) {
    const remote = Number(asset.size ?? asset.file_size);
    const local = expected.sizes.get(asset.name);
    if (Number.isFinite(remote) && remote !== local) {
      throw new Error(`GitCode asset size differs for ${asset.name}`);
    }
  }
}

export async function publishGitcodeRelease({
  token,
  version,
  notes,
  assets,
  repo = DEFAULT_REPO,
  tag = `v${normalizeVersion(version)}`,
  target = "main",
  fetchImpl = fetch,
}) {
  if (!Array.isArray(assets) || assets.length === 0) throw new Error("At least one asset is required");
  validateRepo(repo);
  const files = assets.map((file) => {
    if (!fs.existsSync(file) || !fs.statSync(file).isFile()) throw new Error(`Missing release asset: ${file}`);
    return path.resolve(file);
  });
  const names = files.map((file) => path.basename(file));
  if (new Set(names).size !== names.length) throw new Error("Release asset names must be unique");
  const expected = { tag, names, sizes: new Map(files.map((file) => [path.basename(file), fs.statSync(file).size])) };
  const ctx = { token, repo, tag, fetchImpl };

  let release = await getRelease(ctx);
  if (!release) {
    await createRelease(ctx, notes || `Hoxi Desktop ${tag}`, target);
    release = await getRelease(ctx);
    if (!release) throw new Error("GitCode release was not created");
  } else {
    await setReleaseState(ctx, notes || release.body || `Hoxi Desktop ${tag}`, "pre");
  }

  for (const asset of attachedAssets(release)) {
    if (names.includes(asset.name)) await deleteAttachedAsset(ctx, asset);
  }

  for (const file of files) await uploadAsset(ctx, file);

  release = await getRelease(ctx);
  verifyRelease(release, expected, "pre");
  await setReleaseState(ctx, notes || release.body || `Hoxi Desktop ${tag}`, "latest");
  release = await getRelease(ctx);
  verifyRelease(release, expected, "latest");

  return {
    success: true,
    provider: "gitcode",
    tag,
    url: `https://gitcode.com/${repo}/releases/tag/${tag}`,
    assets: names,
  };
}

async function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      version: { type: "string" },
      notes: { type: "string" },
      repo: { type: "string", default: DEFAULT_REPO },
      tag: { type: "string" },
      target: { type: "string", default: "main" },
      "dry-run": { type: "boolean", default: false },
    },
  });
  if (!values.version) throw new Error("--version is required");
  const version = normalizeVersion(values.version);
  const tag = values.tag || `v${version}`;
  const token = process.env.GITCODE_TOKEN;
  if (!token?.trim()) throw new Error("GITCODE_TOKEN is required");
  if (values["dry-run"]) {
    await gitcodeRequest({ token, method: "GET", url: `${API_BASE}/user` });
    const release = await getRelease({ token, repo: validateRepo(values.repo), tag, fetchImpl: fetch });
    console.log(JSON.stringify({ success: true, provider: "gitcode", tag, exists: Boolean(release),
      assets: attachedAssets(release).map((asset) => asset.name) }));
    return;
  }
  console.log(JSON.stringify(await publishGitcodeRelease({
    token,
    version,
    notes: values.notes,
    assets: positionals,
    repo: values.repo,
    tag,
    target: values.target,
  })));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
