# Telemetry and Network Egress

Hoxi is a local-first, bring-your-own-provider tool. This page describes the
only telemetry Hoxi itself sends, how source builds stay silent by default, and
how to turn off all auxiliary cloud connections. AI prompts, code, diffs, file
paths and session content never go to Hoxi.

## Default behavior by build variant

- **Source & development builds (`go run`, `go build`, `wails dev`, `Hoxi Dev`)**:
  anonymous usage stats default to **off**. No launch event is sent unless you
  explicitly enable it in **Settings → Network & Privacy**.
- **Official packaged release builds (`BuildVariant=release`)**: anonymous usage
  stats default to **on**, and can be disabled at any time via the UI toggle or
  environment variables below.

## Silent offline mode

To silence **all** background auxiliary requests to `hoxi.ai` at once (automatic
update checks, model leaderboard & pricing sync, skills marketplace catalog,
launch telemetry, and background device-token verification), enable **Silent
offline mode** under **Settings → Network & Privacy**, or set:

```bash
HOXI_OFFLINE=1
```

While silent offline mode is active, Hoxi uses built-in local fallbacks and
never contacts `hoxi.ai` in the background. Direct connections to your own
configured AI providers and manual sign-in actions remain unaffected.

## What is sent (when telemetry is enabled)

At application startup the desktop host may POST to
`https://hoxi.ai/api/hoxi/client-events`:

| Field | Value |
| --- | --- |
| `event` | `install` on the first run, otherwise `launch` |
| `installId` | Random local identifier, not derived from hardware or account |
| `package` | `desktop` |
| `os` | `mac`, `linux` or `windows` |
| `arch` | `arm64`, `x64` or the Go architecture name |
| `version` | Hoxi version |

Two headers are added:

- `User-Agent: HoxiDesktop/<version>`
- `Authorization: Bearer <device token>` only when the user is signed in; this
  links the launch to the account the user already chose to sign in with

The request is fire-and-forget with a 3 second timeout. Failures are logged
locally and never block startup. `HOXI_SERVER_URL` can point at a self-hosted
server; otherwise `https://hoxi.ai` is used.

## What is never sent

- Prompts, model output, code, diffs or attachments
- File contents, file paths, project names or session titles
- Approval decisions, tool arguments or terminal output
- API keys, provider credentials or gateway tokens

Update checks, optional sign-in, model price/leaderboard sync and AI provider
requests are separate connections. They are described in the app under
**Settings → Network & Privacy** and on the published privacy notice:
<https://hoxi.ai/legal/privacy-en.html> (Chinese:
<https://hoxi.ai/legal/privacy-zh.html>).

## Local state

| Path | Purpose | Mode |
| --- | --- | --- |
| `~/.hoxi/device-id` | Random install identifier | `0600` |
| `~/.hoxi/telemetry.json` | Explicit telemetry & silent-offline preferences (`enabled`, `offline`) | `0600` |

Deleting `~/.hoxi/device-id` makes the next launch look like a new install.
Deleting `~/.hoxi/telemetry.json` returns to the build-variant default (`off`
for source/dev builds, `on` for packaged release builds) with silent offline
mode off.

## Opting out

1. Open **Settings → Network & Privacy** and switch **Silent offline mode** on
   (to stop all auxiliary `hoxi.ai` requests) or **Anonymous usage stats** off.
   Preferences are stored in `~/.hoxi/telemetry.json` and checked before any
   request is created.
2. Or set environment variables for the app process:

   ```bash
   # Silence all auxiliary background requests to hoxi.ai
   HOXI_OFFLINE=1

   # Or disable only anonymous launch telemetry
   HOXI_DISABLE_TELEMETRY=1
   DO_NOT_TRACK=1
   ```

   `1`, `true`, `yes` and `on` are accepted. Environment overrides win over
   stored preferences, and the corresponding settings toggle is locked while set.

The gateway exposes the same preferences at `GET/PUT /api/hoxi/telemetry`:

```bash
curl -s http://127.0.0.1:<gateway-port>/api/hoxi/telemetry
curl -s -X PUT http://127.0.0.1:<gateway-port>/api/hoxi/telemetry \
  -H 'Content-Type: application/json' -d '{"enabled":false,"offline":true}'
```
