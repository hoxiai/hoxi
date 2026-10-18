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

All paths start with `/api/hoxi/browser/` and require query parameters:

- `directory`: absolute existing directory, canonicalized through symlinks.
- `sessionID`: 1-128 ASCII letters, digits, underscores, dots, or hyphens; first
  character must be alphanumeric.

The pair identifies one independent headless Chrome process and temporary profile.
At most eight scopes are retained, including scopes reserved by `control` without
launching Chrome. `close` frees the slot after process/profile cleanup.

- `GET state`: `{url,title,tabs:[{id,title,url}],tabID,manual}`. An unstarted scope
  returns empty strings, an empty tabs array, and `manual:false`.
- `GET frame`: JPEG of the current viewport, exactly 1280x800; returns 404 if no
  browser has started, including scopes reserved by `control`.
- `POST action`: `Content-Type: application/json`, maximum 1 MiB. Unknown fields,
  trailing JSON, invalid actions, and invalid navigation URLs are rejected.

POST fields are `action`, optional `url`, `selector`, `text`, `key`, `x`, `y`,
`deltaX`, `deltaY`, `tabID`, and `manual`. Every successful action returns state,
with the following action-specific fields:

| Action | Parameters / Behavior |
| --- | --- |
| `navigate` | `url`: only HTTP, HTTPS, or exactly `about:blank` |
| `back`, `forward`, `reload` | Selected tab navigation |
| `click` | CSS `selector`, or viewport coordinates `x`,`y` |
| `type` | Inserts `text` into the focused element via CDP InsertText (IME/paste semantics); optional CSS `selector` focuses an element first; replaces selected text |
| `key` | `key`: single character, DOM name such as `Enter`, or chord such as `Control+a`, `Meta+a`, `Shift+ArrowLeft`; modifiers are Control, Meta, Shift, Alt |
| `scroll` | Viewport `x`,`y`, wheel `deltaX`,`deltaY` |
| `snapshot` | Adds `snapshot:{text,controls:[{selector,tag,text}]}` |
| `screenshot` | Adds `image`: base64 JPEG |
| `evaluate` | Agent only; `text` is a JS expression; adds `result` as a bounded JSON-serialized string |
| `tab_new` | Optional `url`, defaults to `about:blank`; selects new tab |
| `tab_select`, `tab_close` | Required `tabID`, checked against browser's page targets |
| `control` | UI only; required boolean `manual`; does not launch Chrome |
| `close` | Stops Chrome, removes profile and scope; idempotent |

Only POST actions other than `control` and `close` start Chrome. Each operation
has a 30-second deadline including waiting for its scope lock. Scopes serialize
independently; request cancellation does not kill an already launched browser.
Service shutdown cancels work, reaps processes, and removes profiles. Initialization
failure/cancellation removes the failed scope. Responses are non-cacheable.
Closing the last tab first creates and selects an `about:blank` replacement, so
the browser process and scope remain usable. Use `close` to release the scope.

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
- Chrome uses a fresh temporary profile, never the user's daily profile. The
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
- Profiles are ephemeral and scopes have no idle eviction or persistence.

## Verification

```sh
/usr/local/go/bin/go test -race ./internal/browser
HOXI_BROWSER_TEST=1 /usr/local/go/bin/go test -race ./internal/browser -count=1
/usr/local/go/bin/go build ./...
```

The default suite exercises validation, authentication, manual ownership, lazy
startup, scope capacity, lock cancellation, and repeated shutdown without Chrome.
The opt-in smoke test exercises real navigation, typing/clicking, screenshots,
DOM snapshots, popup discovery, multi-request tab attachment, tab operations,
scope isolation, and temporary-profile cleanup.
Additional regressions cover Origin-less UI authorization, focused and selector
text insertion, Control/Meta select-all dispatch, Shift selection, replacement
of selected text, scrolled-viewport screenshot/frame pixels, and last-tab recovery.
Exact-origin and proxy-authority tests cover production Wails and default-deny
HTTP access. Live tab tests verify repeated attach/close cleanup, external target
disappearance reconciliation, and root-browser survival after its initial page closes.
