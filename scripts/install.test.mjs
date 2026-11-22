import { test } from "node:test";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const installSh = path.join(scriptsDir, "install.sh");
const installPs1 = path.join(scriptsDir, "install.ps1");

test("install.sh passes POSIX sh syntax validation and install.ps1 isolates -Portable mode", () => {
  const syntax = spawnSync("sh", ["-n", installSh], { encoding: "utf8" });
  assert.equal(syntax.status, 0, syntax.stderr);

  const psContent = fs.readFileSync(installPs1, "utf8");
  assert.match(psContent, /\.portable/);
  assert.match(psContent, /if \(-not \$Portable\) \{\s*Add-UserPathEntry/m);
});

test("install.sh installs Linux bundle, enforces official SHA-256 over tampered mirrors, and uninstalls cleanly", { skip: process.platform === "win32" }, (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-install-sh-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  const pkgSrc = path.join(root, "pkg-src");
  fs.mkdirSync(pkgSrc, { recursive: true });
  fs.writeFileSync(path.join(pkgSrc, "Hoxi"), "#!/usr/bin/env sh\necho hoxi-0.7.0\n", { mode: 0o755 });
  fs.writeFileSync(
    path.join(pkgSrc, "hoxi.desktop"),
    "[Desktop Entry]\nType=Application\nName=Hoxi\nExec=Hoxi %U\nIcon=hoxi\n"
  );
  fs.writeFileSync(path.join(pkgSrc, "hoxi.png"), "PNGDATA");

  const apiDir = path.join(root, "api/repos/hoxiai/hoxi/releases");
  const officialDir = path.join(root, "official/v0.7.0");
  const validMirrorDir = path.join(root, "mirror-valid/v0.7.0");
  const tamperedMirrorDir = path.join(root, "mirror-tampered/v0.7.0");
  const emptyMirrorDir = path.join(root, "mirror-missing");
  for (const d of [apiDir, officialDir, validMirrorDir, tamperedMirrorDir, emptyMirrorDir]) {
    fs.mkdirSync(d, { recursive: true });
  }

  const assetName = "Hoxi-0.7.0-linux-amd64.tar.gz";
  const officialTar = path.join(officialDir, assetName);
  const tarRes = spawnSync("tar", ["-czf", officialTar, "-C", pkgSrc, "Hoxi", "hoxi.desktop", "hoxi.png"], {
    encoding: "utf8",
  });
  assert.equal(tarRes.status, 0, tarRes.stderr);

  const officialSha = crypto.createHash("sha256").update(fs.readFileSync(officialTar)).digest("hex");
  fs.writeFileSync(path.join(officialDir, `${assetName}.sha256`), `${officialSha}  ${assetName}\n`);
  fs.writeFileSync(path.join(apiDir, "latest"), JSON.stringify({ tag_name: "v0.7.0" }));

  // Mirror directories: even if a malicious mirror puts a forged .sha256 file beside a tampered binary,
  // install.sh must fetch .sha256 exclusively from officialDir and reject tamperedMirrorDir!
  fs.copyFileSync(officialTar, path.join(validMirrorDir, assetName));
  fs.writeFileSync(path.join(tamperedMirrorDir, assetName), "tampered-binary-bytes");
  const forgedSha = crypto.createHash("sha256").update("tampered-binary-bytes").digest("hex");
  fs.writeFileSync(path.join(tamperedMirrorDir, `${assetName}.sha256`), `${forgedSha}  ${assetName}\n`);

  const apiBaseUrl = pathToFileURL(path.join(root, "api")).href;
  const releaseBaseUrl = pathToFileURL(path.join(root, "official")).href;

  const fakeHome = path.join(root, "home");
  const installDir = path.join(fakeHome, ".local/share/hoxi");
  const binDir = path.join(fakeHome, ".local/bin");
  const hoxiState = path.join(fakeHome, ".hoxi");
  fs.mkdirSync(hoxiState, { recursive: true });
  fs.writeFileSync(path.join(hoxiState, "models.json"), '{"kept":true}\n');

  const runInstaller = (args, extraEnv = {}) =>
    spawnSync("sh", [installSh, ...args], {
      encoding: "utf8",
      env: {
        ...process.env,
        HOME: fakeHome,
        XDG_DATA_HOME: path.join(fakeHome, ".local/share"),
        HOXI_HOME: hoxiState,
        HOXI_INSTALL_OS_OVERRIDE: "Linux",
        HOXI_INSTALL_ARCH_OVERRIDE: "x86_64",
        HOXI_INSTALL_API_BASE: apiBaseUrl,
        HOXI_INSTALL_RELEASE_BASE: releaseBaseUrl,
        HOXI_INSTALL_TEST_FILE_URL: "1",
        DISPLAY: "",
        WAYLAND_DISPLAY: "",
        ...extraEnv,
      },
    });

  // 1. Rejects malformed version strings immediately.
  for (const badVer of ["0.7", "0.7.0-rc1", "v0.7.x"]) {
    const bad = runInstaller(["--version", badVer]);
    assert.notEqual(bad.status, 0);
  }

  // 2. Tampered mirror binary MUST fail closed against the official SHA-256 (ignoring forged mirror .sha256).
  const tampered = runInstaller([
    "--install-dir",
    installDir,
    "--bin-dir",
    binDir,
    "--mirror",
    pathToFileURL(path.join(root, "mirror-tampered")).href,
  ]);
  assert.notEqual(tampered.status, 0);
  assert.match(tampered.stderr, /SHA-256 checksum mismatch/);
  assert.equal(fs.existsSync(path.join(installDir, "Hoxi")), false);

  // 3. Valid mirror install succeeds; headless Linux prints `host start` hint.
  const headlessOk = runInstaller([
    "--install-dir",
    installDir,
    "--bin-dir",
    binDir,
    "--mirror",
    pathToFileURL(path.join(root, "mirror-valid")).href,
  ]);
  assert.equal(headlessOk.status, 0, headlessOk.stderr);
  assert.match(headlessOk.stdout, /host start/);
  assert.equal(fs.existsSync(path.join(installDir, "Hoxi")), true);
  assert.equal(fs.lstatSync(path.join(binDir, "hoxi")).isSymbolicLink(), true);

  // 4. Failing mirror automatically falls back to official origin; desktop session writes XDG .desktop entry.
  const desktopOk = runInstaller(
    [
      "--install-dir",
      installDir,
      "--bin-dir",
      binDir,
      "--mirror",
      pathToFileURL(emptyMirrorDir).href,
    ],
    { DISPLAY: ":0" }
  );
  assert.equal(desktopOk.status, 0, desktopOk.stderr);
  const desktopEntryPath = path.join(fakeHome, ".local/share/applications/hoxi.desktop");
  assert.equal(fs.existsSync(desktopEntryPath), true);
  assert.match(fs.readFileSync(desktopEntryPath, "utf8"), /Exec=".*\/Hoxi" %U/);

  // 5. --uninstall removes bundle, desktop entry, and CLI symlink while preserving ~/.hoxi.
  const uninst = runInstaller(["--uninstall", "--install-dir", installDir, "--bin-dir", binDir]);
  assert.equal(uninst.status, 0, uninst.stderr);
  assert.equal(fs.existsSync(installDir), false);
  assert.equal(fs.existsSync(desktopEntryPath), false);
  assert.equal(fs.existsSync(path.join(binDir, "hoxi")), false);
  assert.equal(fs.existsSync(path.join(hoxiState, "models.json")), true);

  // 6. --uninstall --purge also removes ~/.hoxi.
  const purge = runInstaller(["--uninstall", "--purge", "--install-dir", installDir, "--bin-dir", binDir]);
  assert.equal(purge.status, 0, purge.stderr);
  assert.equal(fs.existsSync(hoxiState), false);
});
