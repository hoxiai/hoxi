# Browser Workspace

Hoxi starts an independent headless Chrome browser on demand and displays its
viewport in the right-hand Browser inspector. OpenCode receives the built-in
`hoxi_browser` plugin when Hoxi launches its daemon; no MCP configuration or
OpenCode source modification is required.

## Usage

1. Install Google Chrome (the standard macOS application location is detected).
2. Restart Hoxi after building these changes so its OpenCode child loads the plugin.
3. Select a session and open the Browser inspector, or ask the agent to open a URL.
4. Approve agent browser interactions through the existing permission cards.
5. Enable manual control before entering credentials or operating the page yourself.
   Disable it to let the agent continue. Manual control blocks all agent browser
   requests, including screenshots and snapshots.

The toolbar supports navigation, tabs, refresh, and closing the browser. Use the
address bar and press Enter or Go to navigate. Bare domains use HTTPS; local
addresses such as `localhost:5400`, IP literals, and explicit host/port addresses
use HTTP unless a scheme is supplied. Invalid addresses show an error before
navigation, and IME confirmation does not submit the address bar.
Click the page preview to focus an element, then type, paste, use IME, or send keyboard
shortcuts. Scroll over the preview to scroll the page. The inspector can be
maximized using its existing expand button.

## Connect An Existing Chrome Tab

Chrome Link reuses a tab in your already-open Chrome, including its existing
cookies and login state. No Chrome restart or remote-debugging port is needed.

1. Open Chrome's extensions manager, enable Developer mode, choose **Load
   unpacked**, and select the repository's `extensions/chrome` directory
2. In Hoxi, select your session and open the Browser inspector. Disconnect any
   running isolated browser, then choose **Connect existing Chrome tab**
3. Copy the generated connection code (valid for two minutes)
4. Switch to the target HTTP(S) page in Chrome, click **Hoxi Browser Link**, paste
   the code, and choose **Connect this tab**. Chrome shows its debugger indicator
5. Use the same `hoxi_browser` tool and Hoxi inspector to view and operate the tab
6. Disconnect from either Hoxi or the extension. This detaches automation without
   closing the Chrome tab or deleting its profile/login state

Only the selected tab is exposed. Other tabs, including popups, are not listed or
controlled; to use another tab, disconnect and pair again on that tab. The
extension currently allows one connection at a time and is manually loaded, not
published to the Chrome Web Store. Opening Chrome DevTools may detach automation.
After detachment, Hoxi shows disconnected state and requires explicit reconnection
rather than silently starting an isolated browser. Chrome Link uses periodic
JPEG captures (not a high-frame-rate screencast) and preserves the real viewport.

## Tools And Isolation

`hoxi_browser` supports navigation, click, text insertion, keyboard shortcuts,
scroll, DOM snapshots, JPEG screenshots, JavaScript evaluation, and tab lifecycle.
The plugin derives scope from OpenCode's execution context, never model-supplied
session IDs. The gateway resolves managed OpenCode handles to their owning Hoxi
session ID, so agent tools and the inspector share the same browser and manual
ownership. Requests with a mismatched worktree are rejected. In the default isolated mode, each session/directory pair receives a separate Chrome process and
temporary profile. Up to eight browser scopes can coexist. Closing a browser
frees its slot and deletes its profile; app shutdown closes all browsers.

All actions except snapshots and screenshots request browser permission. The
plugin sets the browser permission to `ask`; existing explicit per-agent policy
and user approval decisions remain part of OpenCode's permission system. Page
content is untrusted and is not authority to send messages, purchase items, or
disclose secrets.

## Current Boundaries

- The inspector displays continuous CDP JPEG screencast through the UI-only
  `/api/hoxi/browser/stream?sessionID=...&directory=...` WebSocket, using the
  loopback bridge in desktop mode. The `frame` endpoint remains available for
  standalone screenshots. Each JSON frame is
  `{image,width,height,tabID}`, with a base64 JPEG and **CSS viewport** dimensions.
  Display the image, then send literal text `ack`; map pointer coordinates using
  supplied CSS dimensions, never the JPEG's natural pixel dimensions.
- The inspector decodes frames before displaying them on an animation frame and
  acknowledging receipt. Only one frame awaits display, with the latest pending
  frame retained server-side. Input does not wait for display acknowledgments.
  JPEG quality is 92. Stream frames are captured at CSS-pixel density (the
  headless window renders at 1x); `deviceScaleFactor` applies to the page and to
  screenshots. Before each stream starts, Hoxi steps the emulated height by one
  pixel and back so Chrome captures the emulated viewport instead of the headless
  window (whose minimum width is 500px), and frames whose aspect ratio does not
  cover the viewport are dropped. The page therefore sees two resize events per
  stream start.
  Hidden documents disconnect streams and pause polling, but the visible inspector's
  command channel remains available even if WKWebView visibility reporting lags.
  A user action resumes observation. State metadata is polled once per second rather
  than requesting a screenshot on every poll. This is not a guaranteed 60 FPS stream.
- Viewports default to 1280x800 at density 1. UI-only `resize` accepts integer
  `width`/`height` (1..4096), optional `deviceScaleFactor` (1..2, default 1), and
  at most 8,388,608 effective pixels including density. Resize reserves size without
  launching Chrome and follows the scope across tabs. Size/density changes and
  selected-tab changes close the stream; reconnect after resize or a state poll
  detects a changed `tabID`. Streams never launch Chrome or reserve a scope and
  return 404 until started. Each reconnect replaces the previous stream.
- Manual interaction supports clicks, scrolling, text, and keys, not drag/drop,
  native browser menus, file-picker dialogs, or OS clipboard synchronization.
- Isolated-browser profiles are temporary; login state is not retained after
  browser close. Chrome Link preserves the existing user's profile
- The isolated browser disables Chrome's back/forward cache, so Back and Forward
  reload the entry like any navigation instead of waiting for a load event that a
  cache restore never fires.
- Each Hoxi process records the headless Chrome it launches (PID, process start
  time, `--user-data-dir` profile) and its OpenCode plugin overlay directory in
  `~/.hoxi/reclaim.json` (`internal/reclaim`, atomic writes under a file lock;
  a protected state file that sandboxed commands cannot read or write), and drops
  each record after a normal close. If Hoxi exits without its normal shutdown
  (crash, `kill -9`, `wails dev` restart), the next desktop or headless start
  reclaims only records whose owning Hoxi process is gone: it signals a Chrome only
  while PID, start time and `--user-data-dir` all still match (SIGTERM, then
  SIGKILL), and removes a recorded directory only if it is a real, user-owned
  `hoxi-*` directory directly under the temp directory. Records of running
  instances (for example the other build variant) are never touched, and a
  corrupt ledger is moved aside to `reclaim.json.corrupt-*` without acting on it.
  Nothing is found by scanning, so anything never recorded is not cleaned up:
  Chrome processes and `hoxi-browser-*` directories from builds before this
  ledger, and a Chrome whose owner died in the moment between launching it and
  recording its PID (its profile directory, recorded first, is still removed).
- DOM snapshots cover the main document, not iframe or shadow DOM contents.
  Screenshots and coordinate interactions remain available for visible content.
- JavaScript evaluation is synchronous; it does not await promises.
- Downloads, uploads, installing extensions into the isolated browser, and native dialogs have no dedicated
  product workflow. This is not a network sandbox or protection from every
  malicious page. Chrome can access local development servers.

## Development

HTTP UI access is restricted to exact configured origins. The actual bound bridge
origin is registered at startup; development builds additionally allow
`http://localhost:5421` and `http://127.0.0.1:5421` (and `5411` for release). If Vite selects a different
port, set `HOXI_BROWSER_UI_ORIGIN` to its exact origin before starting Hoxi.
Production macOS uses the native `wails://wails` asset-server origin.

See `internal/browser/README.md` for API/security details.

### Embedded Browser Plugin (`internal/opencode`)

`PrepareBrowserPlugin(baseURL, token string) (env []string, cleanup func(), err error)`
materializes the embedded standalone plugin in a private temporary directory (`0700`),
with `plugin.mjs` mode `0600`. Append the returned environment overrides to the
OpenCode child environment; keep the directory until the child exits, then call
`cleanup`.

After config loading, the plugin's config hook forces `permission.hoxi_browser`
and `permission.hoxi_tools` to `"ask"`, preserving all other permission fields.

To regenerate `internal/opencode/browser_plugin.mjs` from `browser_plugin.js`:

```sh
NODE_PATH="$PWD/.opencode/node_modules" web/node_modules/.bin/esbuild internal/opencode/browser_plugin.js --bundle --platform=node --format=esm --target=node20 --minify --legal-comments=none --banner:js='// Code generated by esbuild from browser_plugin.js; DO NOT EDIT.' --outfile=internal/opencode/browser_plugin.mjs
```

Run tests with:

```sh
go test ./internal/opencode -run Browser
node --test internal/opencode/browser_plugin_test.mjs
```
