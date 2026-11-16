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
builds the matching `darwin/<arch>` slice, bundling the OpenCode sidecar pinned for
that same platform. Release verification is arch-aware: it asserts the requested
Mach-O slice (arm64 or x86_64) on the app, sidecar and any Sparkle framework.

The runtime `--version`, bootstrap API, update requests, settings page, and both
bundle version fields use the embedded build version. The declared minimum macOS
is at least 13.0 and is raised if the main executable or bundled OpenCode requires
a newer version. This release path verifies both arm64 and amd64 artifacts.

For Windows builds (`build-windows.ps1` / `.github/workflows/release-desktop.yml`),
the release pipeline builds `windows/amd64` (and `windows/arm64` when configured),
bundles the matching pinned `opencode.exe` and `sqlite3.exe` from
`internal/opencode/opencode.lock`, and packages both the NSIS installer
(`Hoxi-<version>-windows-<arch>-setup.exe`) and the portable self-update archive
(`Hoxi-<version>-windows-<arch>.zip`) along with SHA-256 checksums.

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

Pushing a `v<major>.<minor>.<patch>` tag runs the desktop release workflow. It
normalizes the tag version and passes it explicitly to the build. Frontend lint,
unit tests, release/plugin regressions, the web build, Go vet, uncached race tests,
and the desktop-tagged Go build must all pass before signing begins.

Configure these GitHub Actions repository secrets before pushing a release tag:

- `MACOS_CERTIFICATE`: base64-encoded Developer ID Application `.p12`, including its private key.
- `MACOS_CERTIFICATE_PWD`: password protecting the `.p12` export.
- `APPLE_API_KEY`: raw App Store Connect `AuthKey_*.p8` contents.
- `APPLE_API_KEY_ID`: App Store Connect API key ID.
- `APPLE_API_ISSUER`: App Store Connect issuer UUID.

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

See [Auto-Updates](auto-updates.md) for required Sparkle secrets, pinned SDK/OpenCode
archive variables, secure key-file handling, local opt-in builds and the website
appcast contract. Production tag CI requires updater configuration; development
bundles never include the updater. Local builds require a preinstalled verified
OpenCode executable and do not execute a remote installer.

## Regression checks

```sh
node --test scripts/release.test.mjs
npm --prefix web run lint
npm --prefix web test
go test -race ./...
```
