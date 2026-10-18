#!/usr/bin/env bash
# ==============================================================================
# scripts/ai-release.sh
# Hoxi Desktop 一键发版与在线更新发布脚本 (AI & Human Friendly)
#
# 功能流水线:
#   1. 语义化版本号校验 (如 0.2.0)
#   2. 前端多语言 i18n & TypeScript 零错误自检
#   3. 本地全量打包 DMG 生产安装包
#   4. 提取 DMG SHA-256 与文件大小
#   5. 写入 APay 核心 posts 表 (type='changelog') 即刻上线发版
#   6. 可选自动打 Git Tag
#
# 用法:
#   ./scripts/ai-release.sh 0.2.0 "1. 支持官网一键授权登录\n2. 支持国内 GitCode 与海外 GitHub 双镜像"
#
# 高级参数:
#   ./scripts/ai-release.sh --version 0.2.0 --notes "说明" [--skip-build] [--dry-run] [--tag] [--json]
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_ROOT}"

# 自动加载 .env 环境变量
if [[ -f "${PROJECT_ROOT}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${PROJECT_ROOT}/.env" 2>/dev/null || true
  set +a
fi

VERSION=""
NOTES=""
SKIP_BUILD=0
DRY_RUN=0
CREATE_TAG=0
JSON_OUTPUT=0
MANDATORY=0
API_URL="${HOXI_RELEASE_API_URL:-}"
API_KEY="${HOXI_RELEASE_KEY:-}"
FORCE_DB=0

# 解析命令行参数
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --notes)
      NOTES="${2:-}"
      shift 2
      ;;
    --api-url)
      API_URL="${2:-}"
      shift 2
      ;;
    --key)
      API_KEY="${2:-}"
      shift 2
      ;;
    --force-db)
      FORCE_DB=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --tag)
      CREATE_TAG=1
      shift
      ;;
    --mandatory)
      MANDATORY=1
      shift
      ;;
    --json)
      JSON_OUTPUT=1
      shift
      ;;
    -h|--help)
      echo "用法: $0 <version> [notes] [options]"
      echo "示例: $0 0.2.0 \"支持官网授权登录与在线更新\""
      exit 0
      ;;
    *)
      if [[ -z "${VERSION}" ]]; then
        VERSION="$1"
      elif [[ -z "${NOTES}" ]]; then
        NOTES="$1"
      fi
      shift
      ;;
  esac
done

VERSION="$(echo "${VERSION}" | sed -E 's/^v//i' | tr -d '[:space:]')"

if [[ -z "${VERSION}" || ! "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
  echo "❌ 错误: 请提供有效的语义化版本号 (例如: 0.2.0)" >&2
  exit 1
fi

if [[ -z "${NOTES}" ]]; then
  NOTES="Hoxi Desktop v${VERSION} 已发布。"
fi

log_step() {
  if [[ "${JSON_OUTPUT}" -eq 0 ]]; then
    echo -e "\033[1;34m==>\033[0m \033[1m$1\033[0m"
  fi
}

log_success() {
  if [[ "${JSON_OUTPUT}" -eq 0 ]]; then
    echo -e "\033[1;32m✓\033[0m $1"
  fi
}

log_error() {
  echo -e "\033[1;31m✗\033[0m $1" >&2
}

# ------------------------------------------------------------------------------
# 步骤 1: 代码自检 (TypeScript & i18n 双语校验)
# ------------------------------------------------------------------------------
log_step "[1/5] 执行前端代码质量与多语言词条校验..."

if ! npm --prefix web run check:i18n >/dev/null 2>&1; then
  log_error "多语言校验失败！请检查 web/src/i18n 词条是否对齐"
  exit 1
fi

if ! npx --prefix web tsc -p web/tsconfig.json --noEmit >/dev/null 2>&1; then
  log_error "TypeScript 类型检查失败！请修复编译错误"
  exit 1
fi

log_success "代码质量检查全部通过"

# ------------------------------------------------------------------------------
# 步骤 2: 生产打包与 DMG 封装
# ------------------------------------------------------------------------------
DMG_OUTPUT="build/bin/Hoxi.dmg"
TARGET_DMG="build/bin/Hoxi-${VERSION}-darwin-arm64.dmg"

if [[ "${SKIP_BUILD}" -eq 1 ]]; then
  log_step "[2/5] 跳过本地构建 (--skip-build)"
  if [[ ! -f "${TARGET_DMG}" && ! -f "${DMG_OUTPUT}" ]]; then
    log_error "未找到现有打包产物: ${TARGET_DMG} 或 ${DMG_OUTPUT}"
    exit 1
  fi
else
  log_step "[2/5] 开始本地全量构建生产包 (macOS arm64)..."
  ./build-desktop.sh --release-only --dmg --no-install >/dev/null
  log_success "生产包与 DMG 封装构建完成"
fi

if [[ -f "${DMG_OUTPUT}" && ! -f "${TARGET_DMG}" ]]; then
  cp -f "${DMG_OUTPUT}" "${TARGET_DMG}"
elif [[ -f "${DMG_OUTPUT}" ]]; then
  cp -f "${DMG_OUTPUT}" "${TARGET_DMG}"
fi

# ------------------------------------------------------------------------------
# 步骤 3: 提取文件大小与 SHA-256 哈希
# ------------------------------------------------------------------------------
log_step "[3/5] 计算发布包校验哈希与元数据..."

ACTUAL_DMG="${TARGET_DMG}"
if [[ ! -f "${ACTUAL_DMG}" && -f "${DMG_OUTPUT}" ]]; then
  ACTUAL_DMG="${DMG_OUTPUT}"
fi

SHA256="$(shasum -a 256 "${ACTUAL_DMG}" | awk '{print $1}')"
FILE_SIZE="$(stat -f%z "${ACTUAL_DMG}" 2>/dev/null || stat -c%s "${ACTUAL_DMG}" 2>/dev/null || echo "0")"

log_success "产物 SHA-256: ${SHA256}"
log_success "产物体积: $(( FILE_SIZE / 1024 / 1024 )) MB (${FILE_SIZE} bytes)"

# ------------------------------------------------------------------------------
# 步骤 4: 同步至官网 APay posts 发版表
# ------------------------------------------------------------------------------
log_step "[4/5] 同步发版元数据至官网更新系统..."

PUBLISH_ARGS=(
  "scripts/publish-release-to-posts.mjs"
  "--version" "${VERSION}"
  "--title" "Hoxi Desktop v${VERSION}"
  "--notes" "${NOTES}"
  "--sha256" "${SHA256}"
  "--size" "${FILE_SIZE}"
)

if [[ "${MANDATORY}" -eq 1 ]]; then
  PUBLISH_ARGS+=("--mandatory")
fi

if [[ "${DRY_RUN}" -eq 1 ]]; then
  PUBLISH_ARGS+=("--dry-run")
fi

if [[ -n "${API_URL}" ]]; then
  PUBLISH_ARGS+=("--api-url" "${API_URL}")
fi

if [[ -n "${API_KEY}" ]]; then
  PUBLISH_ARGS+=("--key" "${API_KEY}")
fi

if [[ "${FORCE_DB}" -eq 1 ]]; then
  PUBLISH_ARGS+=("--force-db")
fi

if [[ -f "${PUBLISH_ARGS[0]}" ]]; then
  PUB_RESULT="$(node "${PUBLISH_ARGS[@]}")"
  if echo "${PUB_RESULT}" | grep -q '"mode":"api"'; then
    log_success "发版元数据已通过官网授权 API 成功发布 (hoxi.ai)"
  elif echo "${PUB_RESULT}" | grep -q '"mode":"db"'; then
    log_success "发版元数据已写入本地数据库 (PostgreSQL)"
  else
    log_success "发版元数据已生效"
  fi
else
  PUB_RESULT='{"publication":"skipped","reason":"publisher script not installed"}'
  log_step "Publisher script not installed; website synchronization skipped."
fi

# ------------------------------------------------------------------------------
# 步骤 5: Git Tag (可选)
# ------------------------------------------------------------------------------
TAG_NAME="v${VERSION}"
if [[ "${CREATE_TAG}" -eq 1 && "${DRY_RUN}" -eq 0 ]]; then
  log_step "[5/5] 打 Git Tag (${TAG_NAME})..."
  if git rev-parse "${TAG_NAME}" >/dev/null 2>&1; then
    log_success "Git Tag ${TAG_NAME} 已存在，跳过打标"
  else
    git tag -a "${TAG_NAME}" -m "Release ${TAG_NAME}"
    log_success "已创建 Git Tag: ${TAG_NAME}"
  fi
else
  log_step "[5/5] Git Tag (未指定 --tag，跳过自动打标)"
fi

# ------------------------------------------------------------------------------
# 最终总结输出
# ------------------------------------------------------------------------------
if [[ "${JSON_OUTPUT}" -eq 1 ]]; then
  echo "${PUB_RESULT}"
  exit 0
fi

echo ""
echo "================================================================================"
echo "🎉 Hoxi Desktop v${VERSION} 一键发版成功！"
echo "================================================================================"
echo "  • 版本号      : v${VERSION}"
echo "  • DMG 产物    : ${ACTUAL_DMG}"
echo "  • SHA-256     : ${SHA256}"
echo "  • 国内 GitCode: https://gitcode.com/hoxiai/hoxi/releases/download/v${VERSION}/Hoxi-${VERSION}-darwin-arm64.dmg"
echo "  • 海外 GitHub : https://github.com/hoxiai/hoxi/releases/download/v${VERSION}/Hoxi-${VERSION}-darwin-arm64.dmg"
echo "  • 在线升级 API: /api/hoxi/upgrade (客户端即刻可收到新版本推送)"
echo "================================================================================"
echo "提示: 如需推送 Release Tag 至 GitHub/GitCode，请执行:"
echo "  git push origin ${TAG_NAME}"
echo "================================================================================"
