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

# Prefer create-dmg if available for custom icon positioning and visual presentation
if command -v create-dmg >/dev/null 2>&1; then
  echo "📦 Creating DMG using create-dmg: ${DMG_PATH}..."
  create-dmg \
    --volname "${VOLUME_NAME}" \
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

  hdiutil create \
    -volname "${VOLUME_NAME}" \
    -srcfolder "${STAGE_DIR}" \
    -ov \
    -format UDZO \
    "${DMG_PATH}"
fi

# Sign the DMG container if a real Developer ID is provided
if [[ "${SIGN_IDENTITY}" != "-" && -n "${SIGN_IDENTITY}" ]]; then
  echo "🔏 Signing DMG with identity: ${SIGN_IDENTITY}..."
  codesign --force --timestamp --sign "${SIGN_IDENTITY}" "${DMG_PATH}"
  codesign --verify --verbose "${DMG_PATH}"
  echo "✅ Signed DMG successfully."
fi

echo "✅ Generated DMG: ${DMG_PATH}"
