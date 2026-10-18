#!/usr/bin/env bash
# Build, verify and publish a Hoxi release. Dry runs never contact the publisher.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_ROOT}"
if [[ -f .env ]]; then
  set -a
  source .env
  set +a
fi

VERSION=""
NOTES=""
SKIP_BUILD=0
DRY_RUN=0
BUILD_ONLY=0
CREATE_TAG=0
JSON_OUTPUT=0
MANDATORY=0
FORCE_DB=0
API_URL="${HOXI_RELEASE_API_URL:-}"
API_KEY="${HOXI_RELEASE_KEY:-}"

require_value() {
  [[ "$#" -ge 2 && -n "$2" ]] || { echo "$1 requires a value" >&2; exit 1; }
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) require_value "$@"; VERSION="$2"; shift 2 ;;
    --notes) require_value "$@"; NOTES="$2"; shift 2 ;;
    --api-url) require_value "$@"; API_URL="$2"; shift 2 ;;
    --key) require_value "$@"; API_KEY="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --build-only) BUILD_ONLY=1; shift ;;
    --tag) CREATE_TAG=1; shift ;;
    --json) JSON_OUTPUT=1; shift ;;
    --mandatory) MANDATORY=1; shift ;;
    --force-db) FORCE_DB=1; shift ;;
    -h|--help)
      echo 'Usage: scripts/ai-release.sh <major.minor.patch> [notes] [options]'
      echo '  --dry-run       Verify a candidate without publishing or creating a tag'
      echo '  --build-only    Build and verify without publishing (local signing allowed)'
      echo '  --skip-build    Verify and reuse a DMG whose embedded version matches'
      echo '  --tag --json --mandatory --api-url URL --key KEY'
      echo '  --force-db      Explicit legacy maintainer publisher; never a silent fallback'
      exit 0 ;;
    --*) echo "Unknown option: $1" >&2; exit 1 ;;
    *)
      if [[ -z "${VERSION}" ]]; then VERSION="$1"
      elif [[ -z "${NOTES}" ]]; then NOTES="$1"
      else echo "Unexpected argument: $1" >&2; exit 1; fi
      shift ;;
  esac
done

VERSION="$(node scripts/release-artifact.mjs version "${VERSION}")"
if [[ "${BUILD_ONLY}" -eq 1 && "${CREATE_TAG}" -eq 1 ]]; then
  echo '--tag requires a published release; it cannot be used with --build-only' >&2
  exit 1
fi
if [[ -z "${NOTES}" ]]; then NOTES="Hoxi Desktop v${VERSION}"; fi

PUBLISHER="scripts/release-publisher.mjs"
if [[ "${FORCE_DB}" -eq 1 ]]; then PUBLISHER="scripts/publish-release-to-posts.mjs"; fi
if [[ "${DRY_RUN}" -eq 0 && "${BUILD_ONLY}" -eq 0 ]]; then
  [[ -f "${PUBLISHER}" ]] || { echo "Required publisher is missing: ${PUBLISHER}" >&2; exit 1; }
  if [[ "${FORCE_DB}" -eq 0 && -z "${API_KEY}" ]]; then
    echo 'HOXI_RELEASE_KEY is required to publish; use --build-only or --dry-run for local verification' >&2
    exit 1
  fi
fi

# Check code quality before inspecting or publishing a candidate.
npm --prefix web run lint >&2
npm --prefix web test >&2
npm --prefix web run build >&2
go vet ./... >&2
go test -race ./... >&2

DMG_OUTPUT="build/bin/Hoxi.dmg"
TARGET_DMG="build/bin/Hoxi-${VERSION}-darwin-arm64.dmg"
if [[ "${SKIP_BUILD}" -eq 0 ]]; then
  BUILD_ARGS=(--release-only --dmg --no-install --version "${VERSION}")
  if [[ "${DRY_RUN}" -eq 0 && "${BUILD_ONLY}" -eq 0 ]]; then
    BUILD_ARGS+=(--notarize)
  fi
  ./build-desktop.sh "${BUILD_ARGS[@]}" >&2
  SOURCE_DMG="${DMG_OUTPUT}"
elif [[ -f "${TARGET_DMG}" ]]; then
  SOURCE_DMG="${TARGET_DMG}"
else
  SOURCE_DMG="${DMG_OUTPUT}"
fi
[[ -f "${SOURCE_DMG}" ]] || { echo "Missing candidate DMG: ${SOURCE_DMG}" >&2; exit 1; }

# Inspect the actual read-only mounted image, not a sidecar manifest or adjacent app.
METADATA="$(node scripts/release-artifact.mjs verify-dmg "${SOURCE_DMG}" "${VERSION}")"
if [[ "${SOURCE_DMG}" != "${TARGET_DMG}" ]]; then cp -f "${SOURCE_DMG}" "${TARGET_DMG}"; fi
RELEASE_JSON="$(node --input-type=module - "${VERSION}" "${NOTES}" "${METADATA}" "${MANDATORY}" "${TARGET_DMG}" <<'JS'
import fs from 'node:fs';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
const [version, notes, raw, mandatory, artifact] = process.argv.slice(2);
const metadata = JSON.parse(raw);
assert.equal(crypto.createHash('sha256').update(fs.readFileSync(artifact)).digest('hex'), metadata.sha256,
  'Copied artifact no longer matches the verified image');
console.log(JSON.stringify({ ...metadata, version, notes, mandatory: mandatory === '1', artifact }));
JS
)"

if [[ "${DRY_RUN}" -eq 1 || "${BUILD_ONLY}" -eq 1 ]]; then
  PUB_RESULT="$(node --input-type=module - "${RELEASE_JSON}" "${DRY_RUN}" <<'JS'
const [raw, dryRun] = process.argv.slice(2);
console.log(JSON.stringify({ ...JSON.parse(raw), publication: dryRun === '1' ? 'dry-run' : 'not-requested',
  dryRun: dryRun === '1', mode: 'build' }));
JS
)"
else
  node scripts/release-artifact.mjs verify-distribution "${TARGET_DMG}" >&2
  if [[ -n "${HOXI_SPARKLE_DIR:-}${HOXI_SPARKLE_PUBLIC_KEY:-}${HOXI_SPARKLE_FEED_URL:-}" ]]; then
    [[ "${FORCE_DB}" -eq 0 ]] || { echo 'Sparkle requires the confirming API publisher' >&2; exit 1; }
    node scripts/sparkle.mjs sign-update "${TARGET_DMG}" "${VERSION}" "${TARGET_DMG}.sparkle.json" >&2
    RELEASE_JSON="$(node --input-type=module - "${RELEASE_JSON}" "${TARGET_DMG}.sparkle.json" <<'JS'
import fs from 'node:fs';
console.log(JSON.stringify({...JSON.parse(process.argv[2]), sparkle: JSON.parse(fs.readFileSync(process.argv[3], 'utf8'))}));
JS
)"
  fi
  if [[ "${FORCE_DB}" -eq 1 ]]; then
    SHA256="$(node -e 'console.log(JSON.parse(process.argv[1]).sha256)' "${METADATA}")"
    FILE_SIZE="$(node -e 'console.log(JSON.parse(process.argv[1]).size)' "${METADATA}")"
    LEGACY_ARGS=(--version "${VERSION}" --notes "${NOTES}" --sha256 "${SHA256}" --size "${FILE_SIZE}" --force-db)
    if [[ "${MANDATORY}" -eq 1 ]]; then LEGACY_ARGS+=(--mandatory); fi
    PUB_RESULT="$(node "${PUBLISHER}" "${LEGACY_ARGS[@]}")"
  else
    PUB_RESULT="$(printf '%s' "${RELEASE_JSON}" | HOXI_RELEASE_KEY="${API_KEY}" HOXI_RELEASE_API_URL="${API_URL}" node "${PUBLISHER}")"
  fi
  PUB_RESULT="$(printf '%s' "${PUB_RESULT}" | node scripts/release-artifact.mjs verify-publication)"
  if [[ "${CREATE_TAG}" -eq 1 ]]; then
    TAG_NAME="v${VERSION}"
    if git rev-parse "${TAG_NAME}" >/dev/null 2>&1; then
      [[ "$(git rev-list -n 1 "${TAG_NAME}")" == "$(git rev-parse HEAD)" ]] || {
        echo "Release tag already points to a different commit: ${TAG_NAME}" >&2; exit 1;
      }
    else
      git tag -a "${TAG_NAME}" -m "Release ${TAG_NAME}"
    fi
  fi
fi

if [[ "${JSON_OUTPUT}" -eq 1 ]]; then
  printf '%s\n' "${PUB_RESULT}"
elif [[ "${DRY_RUN}" -eq 1 || "${BUILD_ONLY}" -eq 1 ]]; then
  echo "Verified Hoxi ${VERSION}: ${TARGET_DMG} (not published)"
else
  echo "Published Hoxi ${VERSION}: ${TARGET_DMG}"
fi
