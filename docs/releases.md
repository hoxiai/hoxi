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

The runtime `--version`, bootstrap API, update requests, settings page, and both
bundle version fields use the embedded build version. The declared minimum macOS
is at least 13.0 and is raised if the main executable or bundled OpenCode requires
a newer version. This release path currently verifies arm64 artifacts.

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

This script does not upload DMGs to GitHub/GitCode. Before publishing metadata,
make the matching versioned artifacts available at the configured mirror URLs.
Apple signing/notarization and the live publishing service still require a real
maintainer-account acceptance run. Local tests never perform these account actions.

## Regression checks

```sh
node --test scripts/release.test.mjs
npm --prefix web run lint
npm --prefix web test
go test -race ./...
```
