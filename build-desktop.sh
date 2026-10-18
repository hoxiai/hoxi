#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${PROJECT_ROOT}"

BUILD_RELEASE=1
BUILD_DEV=1
INSTALL_RELEASE=1
BUILD_DMG=0
NOTARIZE=0
NOTARY_PROFILE="${NOTARY_PROFILE:-hoxi-notary}"
SIGN_IDENTITY=""
BUILD_VERSION="${HOXI_BUILD_VERSION:-}"
EXTRA_WAILS_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 && -n "$2" ]] || { echo "--version requires a value" >&2; exit 1; }
      BUILD_VERSION="$2"
      shift 2
      ;;
    --release-only)
      BUILD_DEV=0
      shift
      ;;
    --dev-only)
      BUILD_RELEASE=0
      INSTALL_RELEASE=0
      shift
      ;;
    --no-install)
      INSTALL_RELEASE=0
      shift
      ;;
    --dmg)
      BUILD_DMG=1
      shift
      ;;
    --notarize)
      BUILD_DMG=1
      NOTARIZE=1
      shift
      ;;
    --notary-profile)
      NOTARY_PROFILE="$2"
      shift 2
      ;;
    --identity)
      SIGN_IDENTITY="$2"
      shift 2
      ;;
    *)
      EXTRA_WAILS_ARGS+=("$1")
      shift
      ;;
  esac
done

if [[ -n "${BUILD_VERSION}" ]]; then
  BUILD_VERSION="$(node scripts/release-artifact.mjs version "${BUILD_VERSION}")"
else
  BUILD_VERSION="$(node scripts/release-artifact.mjs default-version)"
fi
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"

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

resolve_signing_identity() {
  if [[ -n "${SIGN_IDENTITY}" ]]; then
    echo "${SIGN_IDENTITY}"
    return 0
  fi
  if [[ -n "${HOXI_SIGN_IDENTITY:-}" ]]; then
    echo "${HOXI_SIGN_IDENTITY}"
    return 0
  fi
  # Auto-detect Developer ID Application in Keychain
  local id
  id="$(security find-identity -p codesigning -v 2>/dev/null | grep 'Developer ID Application:' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/' || true)"
  if [[ -n "${id}" ]]; then
    echo "${id}"
    return 0
  fi
  echo "-"
}

CURRENT_IDENTITY="$(resolve_signing_identity)"
if [[ "${NOTARIZE}" -eq 1 && "${CURRENT_IDENTITY}" == "-" ]]; then
  echo "Notarization requires a Developer ID Application signing identity." >&2
  exit 1
fi

bundle_and_sign_macos_app() {
  local app_path="$1"
  local bundle_id="$2"
  local display_name="$3"
  local is_release="$4"

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

  node scripts/release-artifact.mjs configure-app "${app_path}" "${BUILD_VERSION}"

  local target_identity="-"
  if [[ "${is_release}" -eq 1 && "${CURRENT_IDENTITY}" != "-" ]]; then
    target_identity="${CURRENT_IDENTITY}"
  fi

  local entitlements="build/darwin/entitlements.plist"

  if [[ "${target_identity}" != "-" ]]; then
    echo "🔏 Signing with Apple Developer ID: '${target_identity}' (Hardened Runtime enabled)..."
    if [[ -f "${app_path}/Contents/MacOS/opencode" ]]; then
      codesign --force --timestamp --options runtime \
        --entitlements "${entitlements}" \
        --sign "${target_identity}" \
        "${app_path}/Contents/MacOS/opencode"
    fi
    codesign --force --timestamp --options runtime \
      --entitlements "${entitlements}" \
      --sign "${target_identity}" \
      "${app_path}"
    codesign --verify --deep --strict --verbose=1 "${app_path}"
    echo "✅ Verified signature for ${app_path}"
  else
    echo "ℹ️  Signing with Ad-hoc signature (local use only)..."
    codesign --force --deep --sign - "${app_path}"
  fi

  if [[ "${is_release}" -eq 1 ]]; then
    node scripts/release-artifact.mjs verify-app "${app_path}" "${BUILD_VERSION}"
  fi

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
  echo "🔨 Building Local Development App (Hoxi Dev.app with DevTools)..."
  go run github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 build \
    -clean \
    -skipbindings \
    -debug \
    -nosyncgomod \
    -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=dev -X github.com/hoxiai/hoxi/internal/buildinfo.Version=${BUILD_VERSION}" \
    "${EXTRA_WAILS_ARGS[@]+"${EXTRA_WAILS_ARGS[@]}"}"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    DEV_STAGE_APP="${STAGE_DIR}/Hoxi Dev.app"
    rm -rf "${DEV_STAGE_APP}"
    mv "build/bin/Hoxi.app" "${DEV_STAGE_APP}"
    bundle_and_sign_macos_app "${DEV_STAGE_APP}" "ai.hoxi.desktop.dev" "Hoxi Dev" 0
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
    -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=release -X github.com/hoxiai/hoxi/internal/buildinfo.Version=${BUILD_VERSION}" \
    "${EXTRA_WAILS_ARGS[@]+"${EXTRA_WAILS_ARGS[@]}"}"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    APP_PATH="build/bin/Hoxi.app"
    bundle_and_sign_macos_app "${APP_PATH}" "ai.hoxi.desktop" "Hoxi" 1

    if [[ "${BUILD_DMG}" -eq 1 ]]; then
      DMG_PATH="build/bin/Hoxi.dmg"
      ./scripts/package-dmg.sh "${APP_PATH}" "${DMG_PATH}" "Hoxi" "${CURRENT_IDENTITY}"

      if [[ "${NOTARIZE}" -eq 1 ]]; then
        echo "☁️  Submitting ${DMG_PATH} to Apple Notary Service (profile: ${NOTARY_PROFILE})..."
        xcrun notarytool submit "${DMG_PATH}" --keychain-profile "${NOTARY_PROFILE}" --wait
        echo "📎 Stapling notarization ticket to ${DMG_PATH} and ${APP_PATH}..."
        xcrun stapler staple "${DMG_PATH}"
        xcrun stapler validate "${DMG_PATH}"
        xcrun stapler staple "${APP_PATH}"
        xcrun stapler validate "${APP_PATH}"
        echo "🎉 Notarization and Stapling complete! Distribution DMG is ready."
      fi
    fi

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
