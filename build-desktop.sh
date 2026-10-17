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

resolve_opencode_bin() {
  local candidates=(
    "${HOXI_OPENCODE:-}"
    "${HOME}/.hoxi/bin/opencode"
    "${HOME}/.opencode/bin/opencode"
    "/opt/homebrew/bin/opencode"
    "/usr/local/bin/opencode"
    "$(command -v opencode 2>/dev/null || true)"
  )
  for c in "${candidates[@]}"; do
    if [[ -n "${c}" && -x "${c}" ]]; then
      echo "${c}"
      return 0
    fi
  done
  return 1
}

if [[ "$(uname -s)" == "Darwin" ]]; then
  APP_PATH="build/bin/Hoxi.app"
  test -x "${APP_PATH}/Contents/MacOS/Hoxi"

  OPENCODE_SRC="$(resolve_opencode_bin || true)"
  if [[ -z "${OPENCODE_SRC}" ]]; then
    echo "⬇️  opencode not found locally; installing official opencode into ~/.opencode/bin..."
    curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path
    OPENCODE_SRC="${HOME}/.opencode/bin/opencode"
  fi

  if [[ -x "${OPENCODE_SRC}" ]]; then
    cp -L "${OPENCODE_SRC}" "${APP_PATH}/Contents/MacOS/opencode"
    chmod 0755 "${APP_PATH}/Contents/MacOS/opencode"
    codesign --force --deep --sign - "${APP_PATH}"
    echo "✅ Bundled standalone opencode sidecar (${OPENCODE_SRC}) into ${APP_PATH}/Contents/MacOS/opencode"
  fi

  BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${APP_PATH}/Contents/Info.plist")"
  test "${BUNDLE_ID}" = "ai.hoxi.desktop"
  echo "✅ Built self-contained macOS application at ${APP_PATH} (${BUNDLE_ID})"
fi
