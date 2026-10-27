# Isolated Browser Service

The gateway creates this service, registers its routes, and exposes browser token
and shutdown hooks. The app generates the agent credential, prepares the browser
tool plugin environment for OpenCode, and closes the service during shutdown.
The frontend Browser inspector provides screenshot/input controls and manual
ownership. Empty tokens disable agent access; the initial random token is
intentionally undisclosed.

HTTP UI access additionally requires the caller to configure its exact origins
with `SetUIOrigins(origins ...string)`. `New()` trusts no HTTP(S) origins by
default. For example, after determining the actual bridge and Vite addresses:

```go
svc.SetUIOrigins("http://127.0.0.1:42817", "http://localhost:5173")
```

Use the actual assigned ports, not these example values. Both the bridge and
Vite origins must be configured when the development proxy preserves the bridge
Host on Origin-less requests. This method replaces the allowlist atomically;
invalid/non-local origins are ignored. Native Wails trust is built in and needs
no configuration. The gateway/app call sites are outside this package; HTTP
access fails closed until the caller supplies this configuration.

## HTTP API

UI/agent paths start with `/api/hoxi/browser/` and require query parameters
(the extension's authenticated WebSocket is described separately below):

- `directory`: absolute existing directory, canonicalized through symlinks.
- `sessionID`: 1-128 ASCII letters, digits, underscores, dots, or hyphens; first
  character must be alphanumeric.

The pair identifies one independent headless Chrome process and temporary profile.
At most eight scopes are retained, including scopes reserved by `control` or `resize` without
launching Chrome. `close` frees the slot after process/profile cleanup. A
started browser that goes unused for 30 minutes is closed the same way; see
`BROWSER.md` for what counts as use and what always keeps a browser open.

- `GET state`: `{url,title,tabs:[{id,title,url}],tabID,manual}`. An unstarted scope
  returns empty strings and an empty tabs array; `manual` reflects reserved control state.
  A scope closed for inactivity also returns `idleClosed: true` and `idleMinutes`
  until a new scope is created for the same session and directory.
  Started isolated tabs also expose `navigation:{status,url,error,httpStatus,startedAt}`;
  status is `loading`, `ready`, `failed` or `stopped`. State comes from main-document
  CDP events and is independent of screencast delivery. Network errors expose only
  a bounded Chromium error code or a fixed label. HTTP failures retain their page.
- `GET frame`: JPEG of the current viewport, initially 1280x800; returns 404 if no
  browser has started, including scopes reserved by `control` or `resize`.
- `GET stream`: UI-only WebSocket carrying continuous CDP JPEG frames; see below.
  Returns 404 until Chrome has started; never creates or reserves a scope.
- `POST action`: `Content-Type: application/json`, maximum 1 MiB. Unknown fields,
  trailing JSON, invalid actions, and invalid navigation URLs are rejected.

POST fields are `action`, optional `url`, `selector`, `text`, `key`, `x`, `y`,
`deltaX`, `deltaY`, `tabID`, `manual`, `width`, `height`, `deviceScaleFactor`,
`scaleFactor`, `phase`, `button`, `clickCount`, `modifiers` and `async`. Every successful action returns state,
with the following action-specific fields:

| Action | Parameters / Behavior |
| --- | --- |
| `navigate` | `url`: only HTTP, HTTPS, or exactly `about:blank` |
| `back`, `forward`, `reload` | Selected tab navigation |
| `async: true` | UI only, on `navigate`, `back`, `forward` or `reload`; returns acceptance and navigation state immediately after Chrome starts/attaches, with a scope-owned 30-second navigation job; subsequent state polls observe completion/failure |
| `stop_loading` | UI only; stops the selected tab and cancels its navigation job, without launching a browser; old job completions cannot replace the stopped/new navigation state |
| `click` | CSS `selector`, or viewport coordinates `x`,`y` |
| `type` | Inserts `text` into the focused element via CDP InsertText (IME/paste semantics); optional CSS `selector` focuses an element first; replaces selected text |
| `key` | `key`: single character, DOM name such as `Enter`, or chord such as `Control+a`, `Meta+a`, `Shift+ArrowLeft`; modifiers are Control, Meta, Shift, Alt |
| `scroll` | Viewport `x`,`y`, wheel `deltaX`,`deltaY` |
| `pointer` | UI only; `phase`: down/move/up/cancel; down/up include `button` (left/middle/right) and `clickCount` (1..3); optional CDP `modifiers` (0..15); held buttons are tracked and released by Hoxi |
| `wheel` | UI only; viewport `x`,`y` and finite bounded `deltaX`,`deltaY`; horizontal bursts navigate once unless the page owns horizontal scrolling; vertical input scrolls |
| `zoom` | UI only; viewport `x`,`y` and relative `scaleFactor` (0.25..4); sends a native pinch to the remote page; Chrome Link requires the `zoom` capability |
| `context` | UI only; viewport `x`,`y`; adds `context:{canCopy,editable,linkURL}` without selection text; only HTTP(S) links without credentials are returned |
| `copy` | UI only; adds `clipboardText` for the focused input/textarea or document selection; passwords/masked fields return `clipboardError:"protected"`; oversized selections return `clipboardError:"tooLarge"` |
| `paste` | UI only; viewport `x`,`y` and plain `text` (at most 65,536 UTF-16 code units); focuses the editable field at that point, preserves an existing selection and inserts text; readonly/disabled fields and iframe hit targets return `clipboardError:"notEditable"` |
| `snapshot` | Adds `snapshot:{text,controls:[{selector,tag,text}]}` |
| `screenshot` | Adds `image`: base64 JPEG |
| `evaluate` | Agent only; `text` is a JS expression; adds `result` as a bounded JSON-serialized string |
| `tab_new` | Optional `url`, defaults to `about:blank`; selects new tab |
| `tab_select`, `tab_close` | Required `tabID`, checked against browser's page targets |
| `control` | UI only; required boolean `manual`; does not launch Chrome |
| `pair` | UI only; returns `{code,expiresIn:120}` for a one-use Chrome Link connection, rejects replacing an existing browser |
| `resize` | UI only; required integer `width` and `height`, each 1..4096; optional float `deviceScaleFactor` in [1,2], default 1; effective pixel area at most 8,388,608; saves scope size without launching Chrome and resizes the selected existing tab |
| `close` | Stops Chrome, removes profile and scope; idempotent |

Clipboard/context actions never reserve a scope or launch Chrome. They use a fixed
page-side script, not user-supplied JavaScript. The service never accesses the OS
clipboard. The desktop UI reads/writes it only after explicit copy/paste gestures,
serializes page actions behind pending input, and discards replies after scope, tab,
navigation or viewport changes. Selection text is not stored in reactive browser
state. Chrome Link advertises `clipboard`; older extensions return an explicit
reload hint. These actions are rejected for agents and remain outside the tunnel.

`width`, `height`, and `deviceScaleFactor` are only valid on `resize`. The default
viewport is 1280x800 at scale 1. The area bound is
`ceil(width * scale) * ceil(height * scale) <= 8388608`.
Metrics use non-mobile CDP device emulation. Each attached tab tracks applied
dimensions and scale, avoiding repeated emulation on ordinary actions. Background
tabs receive current metrics when next used. State does not expose dimensions;
stream metadata supplies CSS width/height for coordinate mapping, independent of
JPEG natural pixel dimensions. Screenshots also use current density. Resizing does not change
manual ownership or bypass the agent manual-control gate.

### Screencast WebSocket

Connect to `/api/hoxi/browser/stream?sessionID=...&directory=...` using an explicit
allowed Origin, including native Wails origins. The same Host and exact Origin
checks as HTTP apply. Origin-less connections are always denied, even with the
HTTP marker; agents and requests carrying agent credentials are denied. No custom
browser marker is required. The embedding server must support WebSocket upgrades.

Server text messages are JSON `{image,width,height,tabID}`. `image` is base64 JPEG
at quality 92; width and height are CSS viewport dimensions captured at setup.
After decoding and displaying each frame, send exactly the text `ack`. At most one
frame is awaiting display ACK and only the latest pending image is retained.
Chrome screencast frames are ACKed independently by a bounded worker, so slow UI
decoding or input does not starve Chrome. Images exceeding 16 MiB **base64** close
the stream; JSON adds only the metadata envelope. The CDP callback only enqueues
events into a bounded queue; queue overflow closes the stream.

One stream per scope is supported; reconnect replaces the prior stream. A stream
is tied to its setup tab, CSS dimensions, and density. Metric changes, selected-tab
changes (including agent actions), tab release, scope close, and shutdown cancel
it. Clients reconnect after resize or a state poll detects a selected-tab change;
externally disappeared targets are reconciled by state polling. No automatic
backend reconfiguration is performed. A resize omitting density resets it to 1.

Reads are limited to 16 bytes; malformed/binary/unsolicited ACKs close the stream.
Display ACK timeout is 30 seconds, ping interval 15 seconds, pong/read timeout
45 seconds, write/upgrade timeout 5 seconds. Socket closure unblocks readers.
CDP setup/stop runs under the scope gate with bounded deadlines; socket upgrade,
writes, and the stream lifetime never hold that gate. Service/scope cancellation
also cancels listeners and the independent CDP ACK worker.

Only POST actions other than `control`, `resize`, `pointer`, `wheel`, `zoom`, `stop_loading`, and `close` start Chrome. Each operation
has a 30-second deadline including waiting for its scope lock. Scopes serialize
independently; request cancellation does not kill an already launched browser.
Service shutdown cancels work, reaps processes, and removes profiles. Initialization
failure/cancellation removes the failed scope. Responses are non-cacheable.
Closing the last tab first creates and selects an `about:blank` replacement, so
the browser process and scope remain usable. Use `close` to release the scope.

## Chrome Link Extension

`GET /api/hoxi/browser/extension` upgrades only a Chrome extension Origin on a
trusted local Host. Unlike UI/agent routes it takes no query parameters and never
accepts an agent credential. The first WebSocket message must be `{token}` within
five seconds; the 256-bit token comes from the UI-only `pair` action and is consumed
once under the scope lock. Expired, replayed and unknown tokens are rejected.
Only this exact gateway route accepts extension Origins; other gateway routes
retain their existing origin checks. The route is excluded from every remote
transport's allow-list.

The extension receives scoped `Action` messages, returns `{result}` or `{error}`,
and sends `{heartbeat:true}` every 20 seconds. Its service worker attaches only
the tab selected by the user, never enumerates tabs, and exposes no content-script
or external-message interface. The normal OpenCode tool approval flow and manual
ownership checks still precede agent actions. State adds `mode:"chrome"` and
`connected`; detached bridges remain bound until explicitly closed, preventing a
silent fallback to a new Chrome. UI stream captures use the existing bounded ACK
protocol at approximately one capture per 750 ms and never resize the real tab.
`close` detaches the extension and frees the scope without closing the tab.

## Trust Boundaries

UI requests with an Origin require an exact configured HTTP(S) origin (including
scheme, host, and port), or a built-in native origin: `wails://wails`,
`wails://wails.localhost`, or `wails://app.wails.localhost`. There are no arbitrary
localhost-port or HTTP Wails-alias exceptions. Production Wails Host `wails` is
accepted, as are the native local aliases. Request Host is independently checked
for a local loopback/Wails authority to prevent DNS rebinding through arbitrary
hostnames. Hostile Origins are rejected on every route, even with valid credentials.

Origin-less UI fetches must send `X-Hoxi-Browser: true` and
`Sec-Fetch-Site: same-origin`. Their Host must exactly match an authority in the
configured HTTP(S) origins, or a native Wails host. An empty Sec-Fetch-Site is
allowed only for native Wails hosts. `same-site`, `cross-site`, and other values
are rejected for Origin-less requests. Trusted explicit-Origin requests do not
require the browser marker. Configuration is exact: ports/host aliases are not
inferred, and origins must not contain credentials, paths, queries, or fragments.
No CORS/preflight support is installed here.

Agents must send both `Authorization: Bearer <token>` and `X-Hoxi-Agent: true`,
and must not send Origin, Sec-Fetch-Site, or X-Hoxi-Browser. Browser-originated UI requests cannot
switch to agent privileges by adding those headers. While `manual:true`, every
agent request is denied, including state, screenshots, and close. Agents cannot
toggle manual control in either direction. UI actions do not require manual mode.
The embedding server must preserve these headers and bind to loopback.

The caller/plugin **must validate the scope against its actual session context**;
an agent token is service-wide and does not prove ownership of a session ID or
directory. This API deliberately does not depend on upstream session internals.
Local Origin checks are not authentication against malicious native programs,
which can forge HTTP headers. Do not disclose the token to frontend JavaScript.

## Limitations

- Chrome is headless, not an embedded interactive native window. `manual` means
  ownership through the frontend Browser inspector's screenshot/input UI.
- Isolated Chrome uses a fresh temporary profile, never the user's daily profile. The
  known macOS Chrome path is preferred; otherwise chromedp discovers Chrome.
- This is not a network or OS sandbox: HTTP(S) includes local/private networks.
  Explicit navigation URLs are restricted, but page scripts, links, redirects,
  popups, and agent JavaScript are not a comprehensive navigation firewall.
- Snapshots cover the main document's light DOM only, not iframe or shadow-root
  contents. They contain at most 200 visible controls, 32,768 text characters,
  2,048 characters per selector, and 256 per label. Very deep selectors may be
  truncated. Page/tab metadata and screenshots are not DOM snapshots.
- Evaluate is synchronous, does not await promises, and truncates serialized
  output to 65,536 characters. Circular/nonserializable values can fail; truncated
  output is not necessarily valid JSON. CDP cancellation is not JS termination.
- Isolated profiles are ephemeral. Chrome Link preserves the user's existing
  profile but controls only one selected HTTP(S) tab; other tabs and popups require
  a new explicit pairing. Both modes keep no persisted Hoxi connection state

## Verification

```sh
go test -race ./internal/browser
HOXI_BROWSER_TEST=1 go test -race ./internal/browser -count=1
go build ./...
```

The default suite exercises validation, authentication, manual ownership, lazy
startup, scope capacity, lock cancellation, and repeated shutdown without Chrome.
The opt-in smoke test exercises real navigation, typing/clicking, screenshots,
DOM snapshots, popup discovery, multi-request tab attachment, tab operations,
scope isolation, and temporary-profile cleanup.
Additional regressions cover Origin-less UI authorization, focused and selector
text insertion, Control/Meta select-all dispatch, Shift selection, replacement
of selected text, scrolled-viewport screenshot/frame pixels, and last-tab recovery.
Dynamic viewport tests cover validation, lazy reservations, responsive innerWidth/
innerHeight and media queries, JPEG/frame dimensions, existing-tab resizing,
new-tab and popup attachment, and switching back to previously sized tabs.
Exact-origin and proxy-authority tests cover production Wails and default-deny
HTTP access. Live tab tests verify repeated attach/close cleanup, external target
disappearance reconciliation, and root-browser survival after its initial page closes.
Stream tests cover explicit-Origin/Host security, agent denial, lazy startup,
display ACK backpressure/latest-frame delivery, invalid input teardown, and real
Chrome reconnect, input during pending ACK, resize, density, tab changes, and shutdown.
