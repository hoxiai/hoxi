# Sparkle Auto-Updates

## Client Behavior

The existing website JSON upgrade API still supplies the in-app version badge and
release notes. The Update in App action opens Sparkle's localized native UI, which
uses the configured official website appcast for authenticated installation.
Automatic checks are enabled. Background downloads are disabled: Sparkle couples
them to install-on-quit staging, which has no public veto for active agent tasks.
Users confirm downloading and restarting in the native dialog.

On macOS release bundles without a usable Sparkle framework, Update in App uses
the SHA-256 verified DMG self-updater. This fallback downloads and prepares the
update inside the install request rather than opening a native progress window.
The `/api/hoxi/updater/native` status includes its current phase and downloaded /
total bytes so the workspace can display download progress, verification,
installation preparation and restart separately. Downloads without a known size
show bytes and an indeterminate progress bar. Failures remain visible in status,
and a new install attempt clears the previous failure.

The workspace JSON check is not startup-only: it runs at launch, every 30 minutes,
and on window focus, visibility or network recovery. Automatic re-checks share a
10-minute minimum gap, never stack while one is in flight, and a failed background
check keeps the last known-good result instead of replacing it with an error. When
an unattended window discovers a new release it posts one native notification per
version (version number only, no session content); an attended window stays silent
and just shows the header badge.
Discovering, displaying, ignoring, or dismissing an available update leaves the
workspace writable. The standard UI's Install reply is intercepted before Sparkle
can download or stage an update. A busy workspace prompts a native confirmation:
the user may cancel or choose Update Anyway, which drains admission and accepts
the interruption instead of silently dismissing the update.

Only after the user selects Install, and again before relaunch, the gateway closes mutation admission
and checks local processes plus authoritative OpenCode status in known workspace
and worktree directories. Busy, unknown or unreachable runtime state denies the
update unless the user explicitly confirms Update Anyway; the confirmation forces
admission closed without consulting runtime state. New mutations return 503 while
admission is closed. Once extraction starts,
the workspace conservatively remains restart-required even on cancellation/error;
quit/restart before starting new work. This prevents a staged install-on-quit from
interrupting newly admitted tasks. External direct daemon clients and unrelated
terminal jobs are outside this gate. A real signed-app acceptance test is required.

## Packaging Notes

macOS releases ship two DMGs: `Hoxi-<version>-darwin-arm64.dmg` and
`Hoxi-<version>-darwin-amd64.dmg` (Intel). Both are signed and notarized. Only the
arm64 bundle carries the Sparkle framework, because the published appcast has a
single arm64 enclosure; bundling Sparkle into the Intel build would push an arm64
update to an Intel Mac. The Intel bundle therefore uses the arch-aware JSON /
self-updater (the update check reports `arch=x64` and the DMG fallback selects
`darwin-amd64.dmg`), and the release payload registers the Intel DMG as a
`macos`/`x64` asset so the website can serve it. Enabling Sparkle auto-update for
Intel requires a per-arch appcast item on the website (out of this repository).

## Deployment Blocker

The website implementation lives in the separate APay Hoxi theme repository.
Its canonical feed is `https://hoxi.ai/api/hoxi/appcast.xml`; the code must be
deployed and verified before shipping an updater-enabled release. A generic release API success
is deliberately insufficient: publication fails without explicit Sparkle acceptance.
No releases, uploads, or private-key generation are performed by unit tests.

## Local Configuration

Use an independently verified, official Sparkle >= 2.6 distribution extracted locally:

```sh
export HOXI_SPARKLE_DIR=/absolute/path/to/extracted-sparkle
export HOXI_SPARKLE_PUBLIC_KEY='<canonical base64 Ed25519 public key, 32 bytes>'
export HOXI_SPARKLE_FEED_URL=https://hoxi.ai/api/hoxi/appcast.xml
export HOXI_OPENCODE=/absolute/path/to/verified/opencode
./build-desktop.sh --release-only --no-install --notarize --version 0.2.1
```

The SDK directory must contain `Sparkle.framework` and executable `bin/sign_update`.
No SDK is downloaded by local builds. All three Sparkle settings must be present
or all absent; partial configuration fails closed, even for development builds.
Without configuration release bundles use the DMG self-updater. Development bundles always
remove the framework and updater plist keys. The native bridge dynamically loads
the framework; no Sparkle link flag or Go build tag is added.

`ditto` preserves the framework's versioned symlinks. Mach-O binaries, helper apps,
XPC bundles and the framework are signed inside out before the host app. Helpers
retain Downloader's official entitlements; other helpers receive none, per Sparkle's
manual signing guidance. Host JIT entitlements are never applied to them. Use an
official signed SDK, not a locally modified/rebuilt framework. Artifact
verification checks helper presence, support for the requested architecture
(arm64 or x86_64) and the minimum OS of every
bundled Mach-O executable, as well as the existing host/sidecar requirements.

## Update Signing

Provision the existing Sparkle Ed25519 private key through a secret store into an
absolute, owner-owned regular file with mode `0600` (or stricter). Do not put the
key in the repository, command arguments, logs or release artifacts.

```sh
export HOXI_SPARKLE_PRIVATE_KEY_FILE=/secure/location/sparkle-private-key
node scripts/sparkle.mjs sign-update build/bin/Hoxi.dmg 0.2.1 build/bin/sparkle.json
```

Use the key file format accepted by the pinned SDK's `sign_update --ed-key-file`.
The command requires Developer ID and a valid stapled DMG ticket, invokes the
official tool, captures its signature and byte length, and independently verifies
the Ed25519 signature using the configured public key. Sign only after notarization
and stapling: changing even one DMG byte afterward invalidates the signature.

`sparkle.json` is public metadata, not a private key. It contains `version`, `url`,
`feedURL`, `publicKey`, `edSignature` (64-byte canonical base64) and `length`.
The enclosure URL is the exact versioned GitHub DMG. The tag workflow uploads the
same DMG, checksum and Sparkle metadata to GitHub before publication, and the
website advertises the same GitHub-hosted installers. The publication payload also includes `minimumMacOS`, `sha256` and `size` from
artifact verification. The website pins `HOXI_SPARKLE_PUBLIC_KEY` to the same public
key as CI and the app. It verifies the public asset before persisting metadata.
Allow more than 60 seconds at the website reverse proxy; the publisher waits up to
120 seconds for Sparkle publication. The website buffers at most a 512 MiB artifact
and needs memory for both chunks and the assembled artifact plus crypto overhead.

## Website Contract

The authenticated release publication request contains the existing release fields
plus a `sparkle` object with the fields above. The server must validate these fields,
check the uploaded asset exists and matches size/hash/signature, persist them and
atomically publish the corresponding stable HTTPS appcast. Only then respond:

```json
{"success":true,"data":{"version":"0.2.1","sparkleAccepted":true,"sparkle":{"version":"0.2.1","url":"https://github.com/hoxiai/hoxi/releases/download/v0.2.1/Hoxi-0.2.1-darwin-arm64.dmg","feedURL":"https://hoxi.ai/api/hoxi/appcast.xml","publicKey":"<32-byte base64>","edSignature":"<64-byte base64>","length":1234}}}
```

The confirmation must echo the exact validated metadata. `sparkleAccepted: true`
means the feed has been deployed, not merely that unknown JSON fields were ignored.
The public feed must return RSS XML with Sparkle namespace
`http://www.andymatuschak.org/xml-namespaces/sparkle`, an item with
`sparkle:version` and `sparkle:shortVersionString` equal to the release version,
and an enclosure with the exact `url`, `length`, `sparkle:edSignature` and
`type="application/octet-stream"`. Include the verified minimum macOS as
`sparkle:minimumSystemVersion`; escape XML and serve an XML content type. Ensure
the appcast and enclosure remain publicly reachable without authentication.

Acceptance requires a real staging/deployment test of the feed and an update from
an earlier signed application. A successful local mock response cannot establish
this. GitHub's uploaded `sparkle.json` is not itself an appcast.

## CI Configuration

Configure the Apple signing and notarization secrets `MACOS_CERTIFICATE` (base64
PKCS#12 with the Developer ID identity), `MACOS_CERTIFICATE_PWD`, `APPLE_API_KEY`
(raw App Store Connect `AuthKey_*.p8` contents), `APPLE_API_KEY_ID` and
`APPLE_API_ISSUER`. Then configure repository secrets
`HOXI_SPARKLE_PUBLIC_KEY`, `HOXI_SPARKLE_PRIVATE_KEY` (raw existing key-file contents),
`HOXI_RELEASE_KEY`. Configure repository variables `HOXI_SPARKLE_FEED_URL`,
optionally `HOXI_RELEASE_API_URL`, and pinned official archive URLs and independently
verified lowercase SHA-256 values:

- `HOXI_SPARKLE_ARCHIVE_URL`, `HOXI_SPARKLE_ARCHIVE_SHA256`: official Sparkle release archive.

The OpenCode version, per-platform release URLs and SHA-256 digests are pinned in
`internal/opencode/opencode.lock`. Go embeds that file for auto-install, and the
release workflows read it through `node scripts/opencode-lock.mjs` instead of
repository variables, so the bundled sidecar and the runtime fallback cannot
drift. Bump the lock and the sidecar together; CI verifies both.

Missing settings fail the production job. Archives are checked before extraction
or execution. Select immutable version URLs, not latest aliases. CI signs the
final notarized/stapled DMG, uploads the DMG/checksum/public Sparkle metadata to
GitHub, then invokes website publication. Website failure leaves the release
uploaded but does not claim successful feed deployment; resolve the website blocker
before retrying metadata publication. Private-key files and Apple keychains are
removed even on failure. Do not rotate the public key without a migration plan for
already-installed clients.

The local `ai-release.sh` path also adds signed metadata when Sparkle is configured,
but does not upload assets. Maintainers must upload the matching versioned GitHub
assets before invoking it; the website must enforce asset availability.
Its legacy database publisher is not supported for Sparkle releases.
