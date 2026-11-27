# Desktop release verification

Release versions use complete numeric `major.minor.patch` values. The build script
defaults to `wails.json` and accepts `--version` to override the version for a
candidate without editing the project configuration.

## Local candidate

```sh
./build-desktop.sh --release-only --no-install --dmg --identity - --version 0.2.1
./scripts/ai-release.sh 0.2.1 --skip-build --dry-run --json
```

`--dry-run` verifies the mounted DMG and never contacts a publisher or creates a
tag. `--build-only` also builds and verifies a candidate without publishing. Both
modes allow local Ad-hoc signatures and report their non-publication explicitly.
An already named `Hoxi-<version>-darwin-arm64.dmg` takes precedence over a generic
`Hoxi.dmg` when `--skip-build` is used. Its actual contents must match the version.
`ai-release.sh` defaults to `--arch arm64`; pass `--arch amd64` to verify an Intel
candidate (use the matching `--skip-build` DMG name).

`build-desktop.sh` accepts `--arch arm64|amd64` (default: the host architecture) and
builds the matching `darwin/<arch>` slice with the built-in Go Native Agent Engine
and embedded pure-Go SQLite (`internal/sqlitecli`)—omitting external `opencode`
sidecar bundling by default (`HOXI_BUNDLE_OPENCODE=0`, optional opt-in via
`HOXI_BUNDLE_OPENCODE=1`). Release verification is arch-aware: it asserts the
requested Mach-O slice (arm64 or x86_64) on the app, any optional sidecar, and any
Sparkle framework.

The runtime `--version`, bootstrap API, update requests, settings page, and both
bundle version fields use the embedded build version. The declared minimum macOS
is at least 13.0 and is raised if the main executable (or optional bundled sidecar)
requires a newer version. This release path verifies both arm64 and amd64 artifacts.

For Windows builds (`build-windows.ps1` / `.github/workflows/release.yml`),
the release pipeline builds `windows/amd64` and `windows/arm64`
with the built-in Go Native Agent Engine and embedded pure-Go SQLite
(`internal/sqlitecli`) without requiring external `opencode.exe` or `sqlite3.exe`
sidecars by default, and packages both the NSIS installer
(`Hoxi-<version>-windows-<arch>-setup.exe`) and the portable self-update archive
(`Hoxi-<version>-windows-<arch>.zip`) along with SHA-256 checksums.

To produce Windows candidates eligible for in-app updates, provision the
Windows code-signing key through the release signer, configure `signtool.exe`
on PATH and set `HOXI_WINDOWS_SIGN_THUMBPRINT` to its certificate thumbprint
in the current user's certificate store. The Wails Windows post-build hook
signs `Hoxi.exe` **before** NSIS embeds it. The build script signs a bundled
sidecar, if present, and the final setup executable before checksums are made.
An unset thumbprint produces unsigned development artifacts; do not publish
them as update candidates. Existing unsigned installations (and changes of
signing key) must bootstrap via a manually installed signed release.

### Windows interactive acceptance

Use clean Windows 11 x64 and ARM64 desktops and the real signed candidates.
CI builds with the production frontend and runs native Go tests on both
architectures, but does not prove the interactive desktop path.

- Install with NSIS; validate the signer on both the installer and the
  installed executable. Run the prior signed build, update in app, and check
  that the new process has the expected version and restores task state.
  Verify unsigned and different-key packages are rejected before launch, and
  exercise a locked executable and a locked optional sidecar separately.
- Background a workspace with a pending approval or task completion. Click
  the notification from the banner and from Notification Center; it should
  focus the correct session without approving anything. Include an NSIS
  install path with spaces, then repeat with the app closed and a saved
  session. A portable copy without its own registered
  `hoxi://` handler must not send a session link to another installation.
- On an unlocked desktop, run the WebView2 download probe, complete the
  dialogs as directed by the test, and check cancellation and same-name
  behavior. Confirm the separate native Save As flow uses the Windows
  Downloads known folder, including when that folder is redirected.
  ```powershell
  $env:HOXI_WINDOWS_INTERACTIVE_TEST = '1'
  go test -tags desktop,production ./internal/app -run '^TestWindowsWebView2DownloadsInteractive$' -v -timeout 5m
  go test ./internal/computer -run '^TestWindowsInteractiveWindowInspection$' -v -timeout 2m
  ```
- Exercise non-elevated input and capture across mixed-DPI monitors, saving
  and terminal output under non-ASCII paths, and the native Agent with and
  without Git Bash installed. The interactive computer probe does not cover
  real Windows applications or mixed-DPI input by itself.

## Public distribution

Configure the Developer ID signing identity (`HOXI_SIGN_IDENTITY`), a Keychain
notary profile (`NOTARY_PROFILE`, default `hoxi-notary`), and `HOXI_RELEASE_KEY`
using the maintainer's environment or secret store. Never commit credentials.

```sh
./scripts/ai-release.sh 0.2.1 "Release notes" --tag
```

Production publication builds with notarization, validates the DMG signature and
stapled ticket, and requires a confirmed response from the release API before
creating a tag. Reusing a DMG applies the same checks. A missing publisher,
missing API credential, skipped publication, rejected API request, failed staple,
or mismatched embedded version fails the command.

The tracked client is `scripts/release-publisher.mjs`; it has no npm dependencies.
`HOXI_RELEASE_API_URL` can override the HTTPS endpoint. Explicit `--force-db`
retains compatibility with the optional local maintainer script, and fails if that
script is unavailable. There is no automatic database fallback.

The local script does not upload DMGs. The tag workflow uploads the same verified
DMG, checksum and Sparkle metadata to GitHub before publishing website metadata.
The website advertises the GitHub release and GitHub-hosted installers. Apple
signing/notarization and the live publishing service still require a real
maintainer-account acceptance run. Local tests never perform these account actions.

## GitHub tag releases

Pushing a `v<major>.<minor>.<patch>` tag runs the unified desktop release workflow
(`.github/workflows/release.yml`). A fast `preflight` job normalizes the tag version,
validates all required signing/Sparkle/release-center credentials up front, pins one
immutable extension commit SHA (preferring a matching `v*` tag or explicit `work_ref`
input), and verifies that the target tag exists in `HOXI_RELEASE_REPO` (polling briefly
on HTTP 404 to tolerate cross-repo push ordering, while failing immediately on 401/403).

Once `preflight` succeeds, `build-macos` (arm64 + amd64), `build-windows` (amd64 + arm64),
and `build-linux` (amd64) build and verify their artifacts in parallel. Frontend lint,
unit tests, release/plugin regressions, the web build, Go vet, uncached race tests,
and the desktop-tagged Go build must all pass before macOS signing begins. A final
`publish-release` job runs only after every desktop platform succeeds, verifies all
SHA-256 digests, uploads the full multi-platform asset set to GitHub Releases in a
single atomic call, and only then publishes website metadata and Sparkle feed updates.

Configure these GitHub Actions repository secrets before pushing a release tag:

- `MACOS_CERTIFICATE`: base64-encoded Developer ID Application `.p12`, including its private key.
- `MACOS_CERTIFICATE_PWD`: password protecting the `.p12` export.
- `APPLE_API_KEY`: raw App Store Connect `AuthKey_*.p8` contents.
- `APPLE_API_KEY_ID`: App Store Connect API key ID.
- `APPLE_API_ISSUER`: App Store Connect issuer UUID.

When official signed desktop bundles are built in a private release orchestrator repository and published to `hoxiai/hoxi`, configure:

- Repository variable `HOXI_RELEASE_REPO`: target `<owner>/<repo>` for GitHub Release asset uploads (defaults to the current repository when unset; set to `hoxiai/hoxi` in the private orchestrator).
- Repository secret `HOXI_PUBLIC_RELEASE_TOKEN`: fine-grained PAT with `Contents: write` on `HOXI_RELEASE_REPO` (required whenever `HOXI_RELEASE_REPO` differs from the build repository).
- CDN mirror sync (S3-compatible Cloudflare R2 / OSS / COS, see [Auto-Updates](auto-updates.md)): set `HOXI_CDN_PUBLIC_URL` (e.g. `https://cdn.hoxi.dev`) plus secrets `HOXI_CDN_ENDPOINT`, `HOXI_CDN_BUCKET`, `HOXI_CDN_ACCESS_KEY_ID`, and `HOXI_CDN_SECRET_ACCESS_KEY` (or `HOXI_CDN_TOKEN`) so `publish-release` mirrors all 16 desktop release assets to `<HOXI_CDN_PUBLIC_URL>/hoxi-desktop/v<version>/` alongside GitHub Releases.

The public upstream repository (`hoxiai/hoxi`) intentionally omits desktop signing workflows and only keeps the `build-mobile-android` (`Hoxi Remote` APK) release workflow in `.github/workflows/release-cross-platform.yml`. Pushing a `v*` tag to `hoxiai/hoxi` publishes the signed `Hoxi Remote` Android APK, while the private release orchestrator checks out that same `hoxiai/hoxi@v<version>` tag alongside optional commercial extensions to build, sign, notarize, and upload the official desktop installers without colliding with the mobile workflow.

Signing and notarization are mandatory on this path. The temporary signing
keychain is added to the runner's search list and deleted after the job, including
on failure. Missing credentials or failed verification stop publication.

Before creating the release, the workflow mounts each final DMG read-only
to verify the app version, runtime version, architecture, minimum macOS and app
signature. It also verifies each DMG's Developer ID signature and stapled notary
ticket. Both the versioned arm64 and amd64 DMGs, a SHA-256 checksum file and the
signed public `sparkle.json` metadata
are uploaded to GitHub before website metadata publication; missing files fail the
release. The Sparkle enclosure and the website download links use the exact GitHub
DMG. The website must explicitly
confirm Sparkle metadata acceptance and feed deployment, not just generic release
success. Its implementation is absent here and remains a deployment blocker.

See [Auto-Updates](auto-updates.md) for required Sparkle secrets, pinned SDK/sidecar
archive variables, secure key-file handling, local opt-in builds and the website
appcast contract. Production tag CI requires updater configuration; development
bundles never include the updater. Local builds require no external OpenCode binary
by default and never execute a remote installer.

## Mobile dual-edition releases (`Hoxi` vs `Hoxi Remote`)

Hoxi ships two distinct Flutter mobile packages with isolated bundle identifiers
and URL schemes so both apps can coexist on the same device without signature or
secure-storage collisions:

| Edition | Bundle / Application ID | Display Name | URL Scheme | Distribution Channel |
| :--- | :--- | :--- | :--- | :--- |
| **Full Edition** | `ai.hoxi.app` | `Hoxi` | `hoxi://` | Official website CDN (`cdn.hoxi.dev` / `hoxi.dev`, `appKey: "hoxi-mobile"`), Apple App Store / TestFlight, Google Play, HarmonyOS |
| **Open-Source Remote Edition (`mobile/`)** | `ai.hoxi.remote` | `Hoxi Remote` | `hoxi-remote://` | GitHub Releases (`Hoxi-Remote-<version>-android.apk` + `.sha256`, Obtainium compatible); iOS distributed as source for Xcode local signing |

Both editions align their semantic version with desktop releases and derive a
monotonic integer build number using the same `versionCode` formula as macOS
Sparkle (`major * 1_000_000 + minor * 1_000 + patch`, e.g., `0.7.0+7000`).

For the open-source `Hoxi Remote` Android APK built in
`.github/workflows/release-cross-platform.yml` (`build-mobile-android`),
configure these GitHub Actions repository secrets before pushing a `v*` tag:

- `ANDROID_KEYSTORE_BASE64`: base64-encoded release `.jks` keystore.
- `ANDROID_KEYSTORE_PASSWORD`: password for the keystore.
- `ANDROID_KEY_PASSWORD`: password for the signing key (defaults to `ANDROID_KEYSTORE_PASSWORD` when identical).
- `ANDROID_KEY_ALIAS`: key alias inside the keystore (e.g., `hoxi-release`).

The workflow pins Flutter `3.41.8`, verifies `applicationId = "ai.hoxi.remote"`
plus the three mobile architecture/boundary gates and unit tests, signs the
release APK, uploads `Hoxi-Remote-<version>-android.apk` and its `.sha256`
checksum to the GitHub Release, and deletes the temporary keystore and
`key.properties` in an `if: always()` cleanup step.

## Regression checks

```sh
node --test scripts/release.test.mjs
npm --prefix web run lint
npm --prefix web test
go test -race ./...
```
