# Browser Workspace

Hoxi provides three browser modes in the right-hand Browser inspector and for the built-in `hoxi_browser` OpenCode plugin:

1. **Desktop Window (`window`, default)**: Opens a real, visible desktop Chrome window backed by a persistent profile at `~/.hoxi/browser-profile` (`hoxipaths.BrowserProfileDir()`). Sign-ins, cookies, and extensions persist across sessions and restarts, native file pickers/downloads and full Chrome DevTools work out of the box, and the inspector does not force viewport emulation or stream live JPEG frames unless you explicitly toggle **Show live preview**.
2. **Daily Chrome (`chrome`, Chrome Link extension)**: Connects to tabs in your already-open daily Chrome via the unpacked `extensions/chrome` MV3 extension. After the first 2-minute pairing code handshake, a persistent `linkToken` is saved in `chrome.storage.local` and `~/.hoxi/browser-profile/.hoxi-extension-token`, allowing one-click reconnect without entering a new code. Supports opening/switching/closing secondary tabs and automatically follows newly opened child/popup tabs (`openerTabId`).
3. **Headless Sandbox (`headless`, or `HOXI_BROWSER_HEADLESS=1`)**: Runs an isolated headless Chrome with a temporary profile that is deleted on close or after 30 minutes of inactivity, streaming a live CDP JPEG screencast into the inspector panel.

## Usage

1. Install Google Chrome (the standard macOS application location is detected).
2. Restart Hoxi after building these changes so its OpenCode child loads the plugin.
3. Select a session and open the Browser inspector, or ask the agent to open a URL.
4. Approve agent browser interactions through the existing permission cards.
5. Choose **Take over** before entering credentials or completing site verification.
   Deliberate page input also takes control automatically, before input is sent.
   Choose **Return to AI** to let the agent continue. Manual control blocks all agent browser
   requests, including screenshots and snapshots.

The toolbar supports navigation, tabs, refresh, and closing the browser. Use the
address bar and press Enter or Go to navigate. Bare domains use HTTPS; local
addresses such as `localhost:5400`, IP literals, and explicit host/port addresses
use HTTP unless a scheme is supplied. Invalid addresses show an error before
navigation, and IME confirmation does not submit the address bar.
The status bar distinguishes Chrome startup, page loading (with a slow-load notice
after 15 seconds), navigation errors, stopped loading and preview connection state.
The refresh button becomes **Stop loading** during navigation. **Retry** repeats a
failed/stopped navigation, or reconnects the preview without reloading the page when
only frame delivery was interrupted. A missing first frame is reported after eight
seconds; an unchanged static page is not treated as a frozen preview.
Site verification remains page content: Hoxi shows the page and offers manual
takeover, without inferring success or automatically completing a challenge.
Click the page preview to focus an element, then type, paste, use IME, or send keyboard
shortcuts. Scroll over the preview to scroll the page. The inspector can be
maximized using its existing expand button.

Right-click the preview for **Copy selected text**, **Copy link address**, **Paste**
and opening the current page or link in the external browser. **Cmd+C / Ctrl+C**
copies the remote selection to the system clipboard; native paste shortcuts and
IME input continue to work. Opening the menu takes manual control first. Clipboard
access happens only for explicit copy/paste actions, never in the background or
through agent tools. Password/masked fields cannot be copied. Copy/paste supports
plain text up to 65,536 UTF-16 code units; rich text, images and files are unsupported.
The menu's paste targets the field under the pointer, preserving its selection if
already focused. Readonly/disabled fields and iframe hit targets are not pasteable
through this menu. Choose **Show page context menu**, or Shift+right-click, to send
the right click to the page itself. This exposes page-defined menus; Chrome's native
menu is not rendered in the preview. Chrome Link requires the updated extension.

The preview forwards hover, left/middle presses, double/triple clicks and
captured drags, so you can select text, drag page controls and move scrollbars.
Losing pointer capture, hiding the window or switching sessions releases held
buttons. Dragging external files into the page remains unsupported.
Horizontal trackpad swipes go back/forward once per gesture. Horizontal scrollers,
overscroll containment, frames, canvases, video and sliders retain their wheel
input instead. Vertical and diagonal wheel input scroll normally. Pinch zoom
supports macOS WebKit gesture events and Chromium Ctrl+wheel, and zooms the
remote page rather than Hoxi's own interface. Chrome Link needs the updated
extension reloaded in Chrome to support pinch zoom.

## Connect An Existing Chrome Tab

Chrome Link reuses a tab in your already-open Chrome, including its existing
cookies and login state. No Chrome restart or remote-debugging port is needed.

1. Open Chrome's extensions manager, enable Developer mode, choose **Load
   unpacked**, and select the repository's `extensions/chrome` directory
2. In Hoxi, select your session and open the Browser inspector. Disconnect any
   running isolated browser, then choose **Connect existing Chrome tab**
3. Copy the generated connection code (valid for two minutes)
4. Switch to the target HTTP(S) page in Chrome, click **Hoxi Browser Link**, paste
   the code, and choose **Connect this tab**. Chrome shows its debugger indicator.
   After the first handshake, a 64-character hex `linkToken` is saved in
   `~/.hoxi/browser-profile/.hoxi-extension-token` and `chrome.storage.local`,
   allowing one-click **Reconnect** without generating a new code
5. Use the same `hoxi_browser` tool and Hoxi inspector to view and operate the tab,
   open/switch/close secondary tabs (`tab_new`, `tab_select`, `tab_close`), and
   follow newly opened child/popup tabs (`openerTabId`)
6. Disconnect from either Hoxi or the extension. This detaches automation without
   closing the Chrome tab or deleting its profile/login state

The extension attaches to the user-selected tab plus any tabs opened from it via
`tab_new` or child/popup navigation (`openerTabId`), without enumerating unrelated
tabs. The extension allows one active Hoxi connection at a time and is manually
loaded, not published to the Chrome Web Store. Opening Chrome DevTools may detach
automation. After detachment, Hoxi shows disconnected state and requires explicit
reconnection rather than silently starting a new Chrome instance. Chrome Link uses
periodic JPEG captures (quality 80, approximately one frame per 750 ms) rather than
a high-frame-rate CDP screencast. With the default **Fit panel** page size, the
linked tab renders at the inspector's size (`Emulation.setDeviceMetricsOverride`,
so it shows letterboxed in the Chrome window); detaching or choosing **Actual
window size** (`viewport_reset`) restores the tab's own viewport. Extensions older
than 0.3.1 lack the `viewport` capability and keep the real viewport.

## Tools And Isolation

`hoxi_browser` supports navigation, click, text insertion, keyboard shortcuts,
scroll, DOM snapshots, JPEG screenshots, JavaScript evaluation, and tab lifecycle.
The plugin derives scope from OpenCode's execution context, never model-supplied
session IDs. The gateway resolves managed OpenCode handles to their owning Hoxi
session ID, so agent tools and the inspector share the same browser and manual
ownership. Requests with a mismatched worktree are rejected.

By default, Hoxi launches a visible **Desktop Window (`window`)** backed by the
persistent profile at `~/.hoxi/browser-profile`. When running in **Headless Sandbox
(`headless`)** mode, each session/directory pair receives a separate headless Chrome
process and temporary profile. Up to eight browser scopes can coexist. Closing an
isolated headless browser frees its slot and deletes its temporary profile; app
shutdown closes all browsers.

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
- Manual interaction supports pointer hover/drag, clicks, scrolling, history swipes,
  pinch zoom, text, keys, a Hoxi context menu and explicit plain-text copy/paste.
  Native Chrome menus and external file drag/drop into the preview remain unsupported.
- **Uploads, Dialogs, Downloads & Diagnostics**:
  - **File chooser & upload**: CDP file-chooser interception (`Page.setInterceptFileChooserDialog`) surfaces active file inputs in `/api/hoxi/browser/workflows` and uploads files via `POST /api/hoxi/browser/upload` (up to 50 MiB per upload, 1 GiB total transfer cap) or cancels via `action: "upload_cancel"`.
  - **JavaScript dialogs**: `alert` / `confirm` / `prompt` / `beforeunload` dialogs are tracked in `/api/hoxi/browser/workflows` and handled via `POST /api/hoxi/browser/workflows` (`accept`, optional `text`).
  - **Downloads**: Tracked in `/api/hoxi/browser/workflows` (up to 32 items, 512 MiB per file) and managed through `POST /api/hoxi/browser/download-{save,open,reveal,cancel}` with temporary storage recorded in `~/.hoxi/reclaim.json`.
  - **Diagnostics**: Console errors/warnings, uncaught exceptions, and failed HTTP/network requests (up to 100 items per tab, URLs sanitized without query/credentials) are exposed at `GET|POST /api/hoxi/browser/diagnostics` (`POST` clears the buffer).
- Desktop Window (`window`) mode persists cookies and sign-ins in `~/.hoxi/browser-profile`; Headless Sandbox (`headless`) profiles are temporary and deleted on close; Chrome Link preserves the user's daily Chrome profile.
- An isolated browser left unused for 30 minutes is closed the same way as
  `close`: Chrome stops and its temporary profile is deleted, so open pages and
  sign-ins are lost. Use means any UI or agent `state`, `frame` or `action`
  request, plus an open `stream` connection (idle time starts again when the
  stream ends). The inspector polls `state` once per second only while the
  Browser panel shows that session in a visible window, so an open panel counts
  as use, but a closed panel, another session or a hidden window does not. A
  browser is never closed this way while it is under manual control, while Chrome
  Link is pairing or connected, while a stream is open, or while an operation is
  in progress. The check runs once a minute. Until the scope is used again, its
  `state` carries `idleClosed: true` and `idleMinutes`, and the inspector's start
  screen explains why the browser closed.
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
- UI address-bar, back/forward and reload requests opt into asynchronous navigation
  so observation and Stop loading remain responsive. Agent tools retain their
  existing synchronous contract. Chrome Link needs the updated extension reloaded
  for navigation status and loading controls.
- Installing extensions into the isolated headless browser has no dedicated product
  workflow. This is not a network sandbox or protection from every malicious page;
  Chrome can access local development servers.

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
