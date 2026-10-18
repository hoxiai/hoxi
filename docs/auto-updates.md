# Sparkle Auto-Updates

## Deployment Blocker

The website server is not in this repository. The feed route below is an example,
not an assertion that it exists. Do not ship an updater-enabled release until the
website implements and verifies the contract below. A generic release API success
is deliberately insufficient: publication fails without explicit Sparkle acceptance.
No releases, uploads, or private-key generation are performed by unit tests.

## Local Configuration

Use an independently verified, official Sparkle >= 2.6 distribution extracted locally:

```sh
export HOXI_SPARKLE_DIR=/absolute/path/to/extracted-sparkle
export HOXI_SPARKLE_PUBLIC_KEY='<canonical base64 Ed25519 public key, 32 bytes>'
export HOXI_SPARKLE_FEED_URL=https://hoxi.ai/updates/macos/appcast.xml
export HOXI_OPENCODE=/absolute/path/to/verified/opencode
./build-desktop.sh --release-only --no-install --notarize --version 0.2.1
```

The SDK directory must contain `Sparkle.framework` and executable `bin/sign_update`.
No SDK is downloaded by local builds. All three Sparkle settings must be present
or all absent; partial configuration fails closed, even for development builds.
Without configuration release bundles have no updater. Development bundles always
remove the framework and updater plist keys. The native bridge dynamically loads
the framework; no Sparkle link flag or Go build tag is added.

`ditto` preserves the framework's versioned symlinks. Mach-O binaries, helper apps,
XPC bundles and the framework are signed inside out before the host app. Helpers
retain Downloader's official entitlements; other helpers receive none, per Sparkle's
manual signing guidance. Host JIT entitlements are never applied to them. Use an
official signed SDK, not a locally modified/rebuilt framework. Artifact
verification checks helper presence, arm64 support and the minimum OS of every
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
The enclosure URL is the exact versioned GitHub DMG. No GitCode upload is assumed.

## Website Contract

The authenticated release publication request contains the existing release fields
plus a `sparkle` object with the fields above. The server must validate these fields,
check the uploaded asset exists and matches size/hash/signature, persist them and
atomically publish the corresponding stable HTTPS appcast. Only then respond:

```json
{"success":true,"data":{"version":"0.2.1","sparkleAccepted":true,"sparkle":{"version":"0.2.1","url":"https://github.com/hoxiai/hoxi/releases/download/v0.2.1/Hoxi-0.2.1-darwin-arm64.dmg","feedURL":"https://hoxi.ai/updates/macos/appcast.xml","publicKey":"<32-byte base64>","edSignature":"<64-byte base64>","length":1234}}}
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

In addition to Apple credentials, configure repository secrets
`HOXI_SPARKLE_PUBLIC_KEY`, `HOXI_SPARKLE_PRIVATE_KEY` (raw existing key-file contents)
and `HOXI_RELEASE_KEY`. Configure repository variables `HOXI_SPARKLE_FEED_URL`,
optionally `HOXI_RELEASE_API_URL`, and pinned official archive URLs and independently
verified lowercase SHA-256 values:

- `HOXI_SPARKLE_ARCHIVE_URL`, `HOXI_SPARKLE_ARCHIVE_SHA256`: official Sparkle release archive.
- `HOXI_OPENCODE_ARCHIVE_URL`, `HOXI_OPENCODE_ARCHIVE_SHA256`: official macOS arm64 tar.gz with root `opencode` executable.

Missing settings fail the production job. Archives are checked before extraction
or execution. Select immutable version URLs, not latest aliases. CI signs the
final notarized/stapled DMG, uploads the DMG/checksum/public Sparkle metadata to
GitHub, then invokes website publication. Website failure leaves the GitHub asset
uploaded but does not claim successful feed deployment; resolve the website blocker
before retrying metadata publication. Private-key files and Apple keychains are
removed even on failure. Do not rotate the public key without a migration plan for
already-installed clients.

The local `ai-release.sh` path also adds signed metadata when Sparkle is configured,
but does not upload assets. Maintainers must upload the matching versioned GitHub
asset before invoking it; the website must enforce asset availability. Its legacy
database publisher is not supported for Sparkle releases.
