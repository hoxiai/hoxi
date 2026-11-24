#!/usr/bin/env bash
set -euo pipefail

APP_PATH="${1:-}"
DMG_PATH="${2:-}"
VOLUME_NAME="${3:-Hoxi}"
SIGN_IDENTITY="${4:--}"

if [[ -z "${APP_PATH}" || -z "${DMG_PATH}" ]]; then
  echo "Usage: $0 <path-to-app> <output-dmg-path> [volume-name] [signing-identity]" >&2
  exit 1
fi

if [[ ! -d "${APP_PATH}" ]]; then
  echo "❌ Error: App not found at '${APP_PATH}'" >&2
  exit 1
fi

mkdir -p "$(dirname "${DMG_PATH}")"
rm -f "${DMG_PATH}"

APP_ICNS="${APP_PATH}/Contents/Resources/iconfile.icns"

# Prefer create-dmg if available for custom icon positioning and visual presentation
if command -v create-dmg >/dev/null 2>&1; then
  echo "📦 Creating DMG using create-dmg: ${DMG_PATH}..."
  VOLICON_ARGS=()
  if [[ -f "${APP_ICNS}" ]]; then
    VOLICON_ARGS=(--volicon "${APP_ICNS}")
  fi
  create-dmg \
    --volname "${VOLUME_NAME}" \
    "${VOLICON_ARGS[@]+"${VOLICON_ARGS[@]}"}" \
    --window-pos 200 120 \
    --window-size 600 400 \
    --icon-size 100 \
    --icon "$(basename "${APP_PATH}")" 150 190 \
    --hide-extension "$(basename "${APP_PATH}")" \
    --app-drop-link 450 190 \
    --no-internet-enable \
    "${DMG_PATH}" \
    "${APP_PATH}" || {
      echo "⚠️ create-dmg encountered an issue, falling back to standard hdiutil..."
      rm -f "${DMG_PATH}"
    }
fi

# Fallback to zero-dependency native hdiutil if create-dmg was absent or failed
if [[ ! -f "${DMG_PATH}" ]]; then
  echo "📦 Creating DMG using native macOS hdiutil: ${DMG_PATH}..."
  STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/hoxi-dmg.XXXXXX")"
  trap 'rm -rf "${STAGE_DIR}"' EXIT

  cp -R "${APP_PATH}" "${STAGE_DIR}/"
  ln -s /Applications "${STAGE_DIR}/Applications"
  if [[ -f "${APP_ICNS}" ]]; then
    cp "${APP_ICNS}" "${STAGE_DIR}/.VolumeIcon.icns"
    if command -v SetFile >/dev/null 2>&1; then
      SetFile -c icnC "${STAGE_DIR}/.VolumeIcon.icns" || true
      SetFile -a C "${STAGE_DIR}" || true
    fi
  fi

  STAGE_KB="$(du -sk "${STAGE_DIR}" | awk '{print $1}')"
  DMG_SIZE_MB="$(( (STAGE_KB / 1024) + 128 ))"
  if (( DMG_SIZE_MB < 256 )); then
    DMG_SIZE_MB=256
  fi

  HDIUTIL_OK=0
  for attempt in 1 2 3; do
    rm -f "${DMG_PATH}"
    if hdiutil create \
      -volname "${VOLUME_NAME}" \
      -srcfolder "${STAGE_DIR}" \
      -fs HFS+ \
      -size "${DMG_SIZE_MB}m" \
      -ov \
      -format UDZO \
      "${DMG_PATH}"; then
      HDIUTIL_OK=1
      break
    fi
    echo "⚠️ hdiutil create attempt ${attempt}/3 failed; retrying..." >&2
    hdiutil detach "/Volumes/${VOLUME_NAME}" -force >/dev/null 2>&1 || true
    sleep 3
  done
  if [[ "${HDIUTIL_OK}" -ne 1 ]]; then
    echo "❌ Error: hdiutil failed to create DMG after 3 attempts" >&2
    exit 1
  fi
fi

# Sign the DMG container if a real Developer ID is provided
if [[ "${SIGN_IDENTITY}" != "-" && -n "${SIGN_IDENTITY}" ]]; then
  echo "🔏 Signing DMG with identity: ${SIGN_IDENTITY}..."
  codesign --force --timestamp --sign "${SIGN_IDENTITY}" "${DMG_PATH}"
  codesign --verify --verbose "${DMG_PATH}"
  echo "✅ Signed DMG successfully."
fi

echo "✅ Generated DMG: ${DMG_PATH}"
