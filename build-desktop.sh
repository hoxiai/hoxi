#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${PROJECT_ROOT}"

if [[ ! -d web/node_modules ]]; then
  npm --prefix web install
fi
npm --prefix web run build

mkdir -p build

go run github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 build \
  -clean \
  -skipbindings \
  -s \
  -m \
  -nosyncgomod \
  "$@"

if [[ "$(uname -s)" == "Darwin" ]]; then
  APP_PATH="build/bin/Hoxi.app"
  test -x "${APP_PATH}/Contents/MacOS/Hoxi"
  BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${APP_PATH}/Contents/Info.plist")"
  test "${BUNDLE_ID}" = "ai.hoxi.desktop"
  echo "✅ Built macOS application at ${APP_PATH} (${BUNDLE_ID})"
fi
