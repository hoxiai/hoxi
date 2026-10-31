# Browser Service (`internal/browser`)

Package `browser` implements Hoxi's multi-mode Chrome workspace (`window` persistent desktop Chrome window at `~/.hoxi/browser-profile`, `chrome` Chrome Link MV3 extension bridge, and `headless` isolated temporary-profile Chrome) and exposes the `/api/hoxi/browser/*` HTTP/WebSocket API consumed by the frontend Browser inspector and the embedded `hoxi_browser` OpenCode plugin.

For full user-facing behavior, mode semantics, and plugin build instructions, see [`../../BROWSER.md`](../../BROWSER.md).

## Initialization & Origin Trust

- `New()` creates a service without launching Chrome. The initial agent token is random and undisclosed; callers must invoke `SetAgentToken(token)` to enable agent tool access (an empty token disables agent access).
- HTTP UI access requires exact origin configuration via `SetUIOrigins(origins ...string)`. Built-in native Wails origins (`wails://wails`, `wails://wails.localhost`, `wails://app.wails.localhost`) are always trusted on loopback/Wails hosts.
- Origin-less UI requests must send `X-Hoxi-Browser: true` and `Sec-Fetch-Site: same-origin` (or empty `Sec-Fetch-Site` on native Wails hosts).
- Agent requests must send `Authorization: Bearer <token>` and `X-Hoxi-Agent: true`, and must not include browser `Origin`, `Sec-Fetch-Site`, or `X-Hoxi-Browser` headers. While `manual: true`, all agent requests are denied.

## HTTP & WebSocket Endpoints

All UI/agent endpoints under `/api/hoxi/browser/` (except `/api/hoxi/browser/extension`) require query parameters `directory` (canonicalized existing directory) and `sessionID` (1–128 alphanumeric/`_`/`.`/`-`). Up to 8 scopes coexist; unused scopes are reaped after 30 minutes of inactivity (`idleClosed: true`).

| Route | Method | Audience | Description |
| :--- | :--- | :--- | :--- |
| `/api/hoxi/browser/state` | `GET` | UI & Agent | Returns `{mode, url, title, tabs:[{id,title,url}], tabID, manual, navigation?, idleClosed?, idleMinutes?}` |
| `/api/hoxi/browser/frame` | `GET` | UI & Agent | Standalone JPEG screenshot of the current viewport (`404` if not started) |
| `/api/hoxi/browser/stream` | `GET` (WS) | UI only | Continuous JPEG frame stream (`{image, width, height, tabID}`) with client `"ack"` backpressure. Quality is **92** for CDP screencast (`window` / `headless`) and **80** for Chrome Link periodic capture (`~750ms`) and standalone `capture`/`screenshot` actions |
| `/api/hoxi/browser/action` | `POST` | UI & Agent | Executes browser actions (`navigate`, `back`, `forward`, `reload`, `stop_loading`, `click`, `type`, `key`, `scroll`, `pointer`, `wheel`, `zoom`, `context`, `copy`, `paste`, `snapshot`, `screenshot`, `evaluate`, `tab_new`, `tab_select`, `tab_close`, `control`, `pair`, `resize`, `viewport_reset`, `upload_cancel`, `close`) |
| `/api/hoxi/browser/workflows` | `GET` / `POST` | UI only | `GET` returns `{upload?, dialog?, downloads:[], error?}`; `POST` accepts/dismisses active JS dialogs (`{requestID, accept, text?}`) |
| `/api/hoxi/browser/upload` | `POST` | UI only | Multipart file upload for an intercepted file chooser (up to 50 MiB per request, 1 GiB per-scope transfer cap) |
| `/api/hoxi/browser/download-{save,open,reveal,cancel}` | `POST` | UI only | Saves, opens, reveals in Finder/Explorer, or cancels a tracked browser download (`{requestID}`) |
| `/api/hoxi/browser/diagnostics` | `GET` / `POST` | UI only | Returns (and on `POST`, clears) up to 100 console error/warning, uncaught exception, and failed network request diagnostics for the active tab |
| `/api/hoxi/browser/extension` | `GET` (WS) | Chrome Link | Accepts only `chrome-extension://<32-char-id>` Origin on loopback Host. Authenticates first frame `{token, features}` via either a 2-minute one-time `pairToken` or the persisted 64-hex `linkToken` (`~/.hoxi/browser-profile/.hoxi-extension-token`), supports multi-tab (`tab_new`, `tab_select`, `tab_close`), child/popup auto-attach (`openerTabId`), and `viewport` emulation |

## Verification

```sh
go test -race ./internal/browser
HOXI_BROWSER_TEST=1 go test -race ./internal/browser -count=1
```
