#!/usr/bin/env bash
# Export a clean, zero-history open-source snapshot of Hoxi (pure Code mode).
# Excludes closed-source sibling directories (work/, client/, site/), internal
# HQ governance notes (.ai/, verify.sh), local Go workspace bridges (go.work*,
# internal/app/work_bridge.go), build outputs, and runtime caches.
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_DIR="${1:-}"

if [[ -z "${TARGET_DIR}" ]]; then
  echo "Usage: $0 <target-directory>" >&2
  echo "Example: $0 /tmp/hoxi-opensource" >&2
  exit 1
fi

mkdir -p "${TARGET_DIR}"
TARGET_ABS="$(cd "${TARGET_DIR}" && pwd)"

if [[ "${TARGET_ABS}" == "${PROJECT_ROOT}" ]]; then
  echo "Refusing to export onto PROJECT_ROOT itself: ${TARGET_ABS}" >&2
  exit 1
fi

rsync -a --delete \
  --exclude='.git/' \
  --exclude='/work/' \
  --exclude='/client/' \
  --exclude='/site/' \
  --exclude='/.ai/' \
  --exclude='/.hoxi/' \
  --exclude='/.claude/' \
  --exclude='/.opencode/node_modules/' \
  --exclude='/.opencode/bun.lock' \
  --exclude='/.opencode/memory/' \
  --exclude='/verify.sh' \
  --exclude='/go.work' \
  --exclude='/go.work.sum' \
  --exclude='/internal/app/work_bridge.go' \
  --exclude='/web/node_modules/' \
  --exclude='/web/dist/' \
  --exclude='/node_modules/' \
  --exclude='/build/bin/' \
  --exclude='/build/preflight/' \
  --exclude='/build/windows/installer/tmp/' \
  --exclude='/build/windows/installer/wails_tools.nsh' \
  --exclude='/hoxi' \
  --exclude='/cmd/hoxi/hoxi' \
  --exclude='/scripts/publish-*.mjs' \
  --exclude='/.env' \
  --exclude='/.env.*' \
  --exclude='.DS_Store' \
  "${PROJECT_ROOT}/" "${TARGET_ABS}/"

echo "✅ Exported clean Hoxi open-source tree to ${TARGET_ABS}"
