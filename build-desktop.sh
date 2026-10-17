#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${PROJECT_ROOT}"

BUILD_RELEASE=1
BUILD_DEV=1
INSTALL_RELEASE=1
EXTRA_WAILS_ARGS=()

for arg in "$@"; do
  case "${arg}" in
    --release-only)
      BUILD_DEV=0
      ;;
    --dev-only)
      BUILD_RELEASE=0
      INSTALL_RELEASE=0
      ;;
    --no-install)
      INSTALL_RELEASE=0
      ;;
    *)
      EXTRA_WAILS_ARGS+=("${arg}")
      ;;
  esac
done

if [[ ! -d web/node_modules ]]; then
  npm --prefix web install
fi
npm --prefix web run build

mkdir -p build/bin

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

OPENCODE_SRC="$(resolve_opencode_bin || true)"
if [[ -z "${OPENCODE_SRC}" && "$(uname -s)" == "Darwin" ]]; then
  echo "⬇️  opencode not found locally; installing official opencode into ~/.opencode/bin..."
  curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path
  OPENCODE_SRC="${HOME}/.opencode/bin/opencode"
fi

bundle_and_sign_macos_app() {
  local app_path="$1"
  local bundle_id="$2"
  local display_name="$3"

  test -x "${app_path}/Contents/MacOS/Hoxi"

  /usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier ${bundle_id}" "${app_path}/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleName ${display_name}" "${app_path}/Contents/Info.plist"
  if /usr/libexec/PlistBuddy -c "Print :CFBundleDisplayName" "${app_path}/Contents/Info.plist" >/dev/null 2>&1; then
    /usr/libexec/PlistBuddy -c "Set :CFBundleDisplayName ${display_name}" "${app_path}/Contents/Info.plist"
  else
    /usr/libexec/PlistBuddy -c "Add :CFBundleDisplayName string ${display_name}" "${app_path}/Contents/Info.plist"
  fi

  if [[ -n "${OPENCODE_SRC}" && -x "${OPENCODE_SRC}" ]]; then
    cp -L "${OPENCODE_SRC}" "${app_path}/Contents/MacOS/opencode"
    chmod 0755 "${app_path}/Contents/MacOS/opencode"
    echo "✅ Bundled standalone opencode sidecar (${OPENCODE_SRC}) into ${app_path}/Contents/MacOS/opencode"
  fi

  codesign --force --deep --sign - "${app_path}"

  local actual_id
  actual_id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${app_path}/Contents/Info.plist")"
  test "${actual_id}" = "${bundle_id}"
  echo "✅ Built macOS application at ${app_path} (${actual_id})"
}

STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/hoxi-build.XXXXXX")"
trap 'rm -rf "${STAGE_DIR}"' EXIT

if [[ "${BUILD_DEV}" -eq 0 && -d "build/bin/Hoxi Dev.app" ]]; then
  mv "build/bin/Hoxi Dev.app" "${STAGE_DIR}/Hoxi Dev.app"
fi

if [[ "${BUILD_DEV}" -eq 1 ]]; then
  echo "🔨 Building Local Development App (Hoxi Dev.app with DevTools & independent instance lock)..."
  go run github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 build \
    -clean \
    -skipbindings \
    -debug \
    -nosyncgomod \
    -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=dev" \
    "${EXTRA_WAILS_ARGS[@]+"${EXTRA_WAILS_ARGS[@]}"}"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    DEV_STAGE_APP="${STAGE_DIR}/Hoxi Dev.app"
    rm -rf "${DEV_STAGE_APP}"
    mv "build/bin/Hoxi.app" "${DEV_STAGE_APP}"
    bundle_and_sign_macos_app "${DEV_STAGE_APP}" "ai.hoxi.desktop.dev" "Hoxi Dev"
  fi
fi

if [[ "${BUILD_RELEASE}" -eq 1 ]]; then
  echo "🚀 Building Production Release App (Hoxi.app)..."
  go run github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 build \
    -clean \
    -skipbindings \
    -s \
    -m \
    -nosyncgomod \
    -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=release" \
    "${EXTRA_WAILS_ARGS[@]+"${EXTRA_WAILS_ARGS[@]}"}"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    APP_PATH="build/bin/Hoxi.app"
    bundle_and_sign_macos_app "${APP_PATH}" "ai.hoxi.desktop" "Hoxi"

    if [[ "${INSTALL_RELEASE}" -eq 1 && -d "/Applications" && -w "/Applications" ]]; then
      INSTALL_PATH="/Applications/Hoxi.app"
      rm -rf "${INSTALL_PATH}"
      ditto "${APP_PATH}" "${INSTALL_PATH}"
      echo "✅ Installed Production Release to ${INSTALL_PATH} (ai.hoxi.desktop)"
    fi
  fi
fi

if [[ -d "${STAGE_DIR}/Hoxi Dev.app" && "$(uname -s)" == "Darwin" ]]; then
  DEV_APP_PATH="build/bin/Hoxi Dev.app"
  rm -rf "${DEV_APP_PATH}"
  mv "${STAGE_DIR}/Hoxi Dev.app" "${DEV_APP_PATH}"
  echo "✅ Local Development App ready at ${DEV_APP_PATH} (ai.hoxi.desktop.dev)"
fi

