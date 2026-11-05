#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
PROJECT_NAME=$(basename "$PWD")
echo "==> [$PROJECT_NAME Gate 1/2] Go 后端静态检查与测试..."
if [[ -d "tools/contractcheck" ]]; then
  go run ./tools/contractcheck
fi
go vet ./...
go test ./...

echo "==> [$PROJECT_NAME Gate 2/2] 前端工程检查..."
if [[ -f "web/package.json" ]]; then
  npm --prefix web run lint
  npm --prefix web test
  npm --prefix web run build
elif [[ -f "admin/package.json" ]]; then
  if grep -q '"type-check"' "admin/package.json"; then
    npm --prefix admin run type-check
  fi
  npm --prefix admin run build
fi
echo "✅ $PROJECT_NAME 门禁验证通过"
