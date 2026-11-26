#!/usr/bin/env bash
# Export a clean, zero-history open-source snapshot of Hoxi (pure Code mode + mobile/).
# Excludes closed-source sibling directories (work/, client/, site/), internal
# HQ governance notes (.ai/, verify.sh), local Go workspace bridges (go.work*,
# internal/app/work_bridge.go), build outputs, and runtime caches, then exports
# the zero-fork open-source Flutter mobile controller into <target>/mobile/ and
# verifies the entire tree with a zero-leak clean-room scan.
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_DIR=""
RUN_VERIFY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --verify)
      RUN_VERIFY=1
      shift
      ;;
    -h|--help)
      echo "Usage: $0 <target-directory> [--verify]" >&2
      echo "  --verify   Run GOWORK=off go vet/test, frontend lint/test/build, and mobile OSS analyze/test inside exported tree" >&2
      exit 0
      ;;
    *)
      if [[ -z "${TARGET_DIR}" ]]; then
        TARGET_DIR="$1"
        shift
      else
        echo "Unexpected argument: $1" >&2
        exit 1
      fi
      ;;
  esac
done

if [[ -z "${TARGET_DIR}" ]]; then
  echo "Usage: $0 <target-directory> [--verify]" >&2
  echo "Example: $0 /tmp/hoxi-opensource --verify" >&2
  exit 1
fi

if [[ "${HOXI_ALLOW_DIRTY_EXPORT:-0}" != "1" ]] && [[ -n "$(git -C "${PROJECT_ROOT}" status --porcelain)" ]]; then
  echo "Refusing to export from a dirty working tree (commit or stash changes first, or set HOXI_ALLOW_DIRTY_EXPORT=1 for dry-run inspection):" >&2
  git -C "${PROJECT_ROOT}" status --short >&2
  exit 1
fi

mkdir -p "${TARGET_DIR}"
TARGET_ABS="$(cd "${TARGET_DIR}" && pwd)"

if [[ "${TARGET_ABS}" == "${PROJECT_ROOT}" ]]; then
  echo "Refusing to export onto PROJECT_ROOT itself: ${TARGET_ABS}" >&2
  exit 1
fi

rsync -a --delete \
  --exclude='.DS_Store' \
  --exclude='.git/' \
  --exclude='/.vscode/' \
  --exclude='/.idea/' \
  --exclude='/work/' \
  --exclude='/client/' \
  --exclude='/mobile/' \
  --exclude='/site/' \
  --exclude='/.ai/' \
  --exclude='/.agents/' \
  --exclude='/.claude/' \
  --exclude='/.opencode/' \
  --include='/.hoxi/' \
  --include='/.hoxi/skills/***' \
  --exclude='/.hoxi/*' \
  --exclude='/verify.sh' \
  --exclude='/go.work' \
  --exclude='/go.work.sum' \
  --exclude='/internal/app/work_bridge.go' \
  --exclude='/web/node_modules/' \
  --exclude='/web/dist/' \
  --exclude='/node_modules/' \
  --exclude='/build/bin/' \
  --exclude='/build/preflight/' \
  --exclude='/build/release-*/' \
  --exclude='/build/windows/wails.exe.manifest' \
  --exclude='/build/windows/installer/tmp/' \
  --exclude='/build/windows/installer/wails_tools.nsh' \
  --exclude='/hoxi' \
  --exclude='/cmd/hoxi/hoxi' \
  --exclude='/scripts/publish-*.mjs' \
  --exclude='.env' \
  --exclude='.env.*' \
  --exclude='*.jks' \
  --exclude='*.keystore' \
  --exclude='*.p12' \
  --exclude='*.pfx' \
  --exclude='*.pem' \
  --exclude='*.key' \
  --exclude='*.p8' \
  --exclude='*.cer' \
  --exclude='*.mobileprovision' \
  --exclude='*.test' \
  --exclude='*.prof' \
  --exclude='key.properties' \
  --exclude='local.properties' \
  "${PROJECT_ROOT}/" "${TARGET_ABS}/"

python3 "${PROJECT_ROOT}/client/scripts/export_mobile_oss.py" "${TARGET_ABS}/mobile"
python3 "${PROJECT_ROOT}/client/scripts/verify_oss_root.py" "${TARGET_ABS}"

if [[ "${RUN_VERIFY}" -eq 1 ]]; then
  echo "==> Verifying exported OSS tree in ${TARGET_ABS} (GOWORK=off)..."
  (
    cd "${TARGET_ABS}"
    mkdir -p web/dist && touch web/dist/placeholder
    GOWORK=off go vet ./...
    GOWORK=off go test ./...
    GOWORK=off go build ./cmd/hoxi
    rm -rf web/dist hoxi
    npm --prefix web ci
    npm --prefix web run lint
    npm --prefix web test
    if [[ -f "scripts/install.test.mjs" ]]; then
      node --test scripts/release.test.mjs scripts/install.test.mjs
    else
      node --test scripts/release.test.mjs
    fi
    npm --prefix web run build
    rm -rf web/node_modules web/dist
    if command -v flutter >/dev/null 2>&1 && [[ -d "mobile" ]]; then
      (
        cd mobile
        (flutter pub get --offline >/dev/null 2>&1 || flutter pub get >/dev/null)
        flutter analyze --no-pub
        flutter test --no-pub
        rm -rf .dart_tool build pubspec.lock .flutter-plugins .flutter-plugins-dependencies
      )
    fi
  )
fi

echo "✅ Exported clean Hoxi open-source tree (desktop + mobile/) to ${TARGET_ABS}"
