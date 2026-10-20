#!/usr/bin/env bash
set -euo pipefail

PROFILE_NAME="${1:-hoxi-notary}"
TEAM_ID="${2:-${APPLE_TEAM_ID:-}}"
if [ -z "${TEAM_ID}" ]; then
  echo "Usage: $0 [profile-name] <apple-team-id>" >&2
  echo "Set the Apple Team ID as the second argument or via APPLE_TEAM_ID." >&2
  exit 1
fi

echo "🔐 Configuring Apple Notarization Keychain Profile: '${PROFILE_NAME}'"
echo "Team ID: ${TEAM_ID}"
echo ""
echo "Note: You will be asked for:"
echo "  1. Your Apple ID email (e.g., developer@yourdomain.com)"
echo "  2. An App-Specific Password (generate at https://appleid.apple.com -> Sign-In and Security -> App-Specific Passwords)"
echo ""

xcrun notarytool store-credentials "${PROFILE_NAME}" --team-id "${TEAM_ID}"

echo ""
echo "✅ Credentials saved securely to macOS Keychain under profile '${PROFILE_NAME}'."
echo "You can now build, sign, package DMG, and notarize with:"
echo "  ./build-desktop.sh --release-only --dmg --notarize --notary-profile ${PROFILE_NAME}"
