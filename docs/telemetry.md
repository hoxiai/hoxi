# Telemetry and Network Egress

Hoxi is a local-first, bring-your-own-provider tool. This page describes the
only telemetry Hoxi itself sends, and how to turn it off. AI prompts, code,
diffs, file paths and session content never go to Hoxi.

## What is sent

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
| `~/.hoxi/telemetry.json` | Explicit opt-in/opt-out preference | `0600` |

Deleting `~/.hoxi/device-id` makes the next launch look like a new install.
Deleting `~/.hoxi/telemetry.json` returns to the default (enabled).

## Opting out

1. Open **Settings → Network & Privacy** and switch **Anonymous usage stats** off.
   The preference is stored in `~/.hoxi/telemetry.json` and checked before any
   request is created.
2. Or set an environment variable for the app process:

   ```bash
   HOXI_DISABLE_TELEMETRY=1
   # or the cross-tool convention
   DO_NOT_TRACK=1
   ```

   `1`, `true`, `yes` and `on` are accepted. The environment override wins over
   the stored preference, and the settings toggle is disabled while it is set.
   For a Finder-launched app, set it in the launch environment
   (`launchctl setenv HOXI_DISABLE_TELEMETRY 1` and relaunch), or start Hoxi from
   a terminal.

The gateway exposes the same preference at `GET/PUT /api/hoxi/telemetry`:

```bash
curl -s http://127.0.0.1:<gateway-port>/api/hoxi/telemetry
curl -s -X PUT http://127.0.0.1:<gateway-port>/api/hoxi/telemetry \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
```

Opting out affects only future events. It does not delete the install
identifier, and it does not stop a request already in flight.
