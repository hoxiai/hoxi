#!/usr/bin/env bash
set -euo pipefail

PROFILE_NAME="${1:-hoxi-notary}"
TEAM_ID="${2:-8ER9ZL9ZFL}"

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
