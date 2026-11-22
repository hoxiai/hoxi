#!/usr/bin/env sh
# Cross-platform installer & uninstaller for Hoxi (macOS & Linux).
set -eu

VERSION=""
INSTALL_DIR=""
BIN_DIR=""
PROXY_URL=""
MIRROR_PREFIX=""
INSTALL_CLI=1
UNINSTALL=0
PURGE=0
NO_LAUNCH_HINT=0

usage() {
  cat <<'EOF'
Hoxi Installer (macOS & Linux)

Options:
  --version <x.y.z>      Install a specific release version (default: latest)
  --install-dir <dir>    Target application directory
  --bin-dir <dir>        Target directory for the 'hoxi' CLI symlink
  --proxy <url>          HTTP/HTTPS/SOCKS5 proxy URL for downloads
  --mirror <prefix>      Download mirror prefix for GitHub release archives
                         (SHA-256 checksums are always verified from official origin)
  --no-cli               Skip creating the 'hoxi' command symlink
  --uninstall            Remove Hoxi application and CLI symlink (keeps ~/.hoxi)
  --purge                With --uninstall, also remove ~/.hoxi user data
  -h, --help             Show this help message
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || { echo "Error: --version requires a value" >&2; exit 1; }
      VERSION="$2"
      shift 2
      ;;
    --install-dir)
      [ $# -ge 2 ] || { echo "Error: --install-dir requires a value" >&2; exit 1; }
      INSTALL_DIR="$2"
      shift 2
      ;;
    --bin-dir)
      [ $# -ge 2 ] || { echo "Error: --bin-dir requires a value" >&2; exit 1; }
      BIN_DIR="$2"
      shift 2
      ;;
    --proxy)
      [ $# -ge 2 ] || { echo "Error: --proxy requires a value" >&2; exit 1; }
      PROXY_URL="$2"
      shift 2
      ;;
    --mirror)
      [ $# -ge 2 ] || { echo "Error: --mirror requires a value" >&2; exit 1; }
      MIRROR_PREFIX="$2"
      shift 2
      ;;
    --no-cli)
      INSTALL_CLI=0
      shift
      ;;
    --uninstall)
      UNINSTALL=1
      shift
      ;;
    --purge)
      PURGE=1
      shift
      ;;
    --quiet-hints)
      NO_LAUNCH_HINT=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Error: unknown option '$1'" >&2
      usage >&2
      exit 1
      ;;
  esac
done

OS_RAW="${HOXI_INSTALL_OS_OVERRIDE:-$(uname -s)}"
ARCH_RAW="${HOXI_INSTALL_ARCH_OVERRIDE:-$(uname -m)}"

case "$OS_RAW" in
  Darwin|darwin) PLATFORM="darwin" ;;
  Linux|linux)   PLATFORM="linux" ;;
  *)
    echo "Error: unsupported operating system '$OS_RAW' (use scripts/install.ps1 on Windows)" >&2
    exit 1
    ;;
esac

case "$ARCH_RAW" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64)  ARCH="amd64" ;;
  *)
    echo "Error: unsupported CPU architecture '$ARCH_RAW'" >&2
    exit 1
    ;;
esac

if [ "$PLATFORM" = "linux" ] && [ "$ARCH" != "amd64" ]; then
  echo "Error: prebuilt Linux releases currently target x86_64 (amd64); build from source for $ARCH" >&2
  exit 1
fi

HOME_DIR="${HOME:-}"
if [ -z "$HOME_DIR" ]; then
  echo "Error: HOME environment variable is not set" >&2
  exit 1
fi

XDG_DATA="${XDG_DATA_HOME:-$HOME_DIR/.local/share}"
SYS_APPS_DIR="/Applications"
USR_LOCAL_BIN="/usr/local/bin"
OPT_BREW_BIN="/opt/homebrew/bin"

resolve_default_install_dir() {
  if [ -n "$INSTALL_DIR" ]; then
    printf '%s\n' "$INSTALL_DIR"
    return
  fi
  if [ "$PLATFORM" = "darwin" ]; then
    if [ -d "$SYS_APPS_DIR" ] && [ -w "$SYS_APPS_DIR" ]; then
      printf '%s\n' "$SYS_APPS_DIR"
    else
      printf '%s\n' "$HOME_DIR/Applications"
    fi
  else
    printf '%s\n' "$XDG_DATA/hoxi"
  fi
}

resolve_default_bin_dir() {
  if [ -n "$BIN_DIR" ]; then
    printf '%s\n' "$BIN_DIR"
    return
  fi
  if [ "$PLATFORM" = "darwin" ] && [ -d "$USR_LOCAL_BIN" ] && [ -w "$USR_LOCAL_BIN" ]; then
    case ":${PATH:-}:" in
      *":$USR_LOCAL_BIN:"*)
        printf '%s\n' "$USR_LOCAL_BIN"
        return
        ;;
    esac
  fi
  printf '%s\n' "$HOME_DIR/.local/bin"
}

remove_managed_symlink() {
  link_candidate="$1"
  if [ ! -L "$link_candidate" ]; then
    return 0
  fi
  link_target="$(readlink "$link_candidate" 2>/dev/null || true)"
  case "$link_target" in
    */Hoxi|*/hoxi|*/Hoxi.app/Contents/MacOS/Hoxi)
      rm -f "$link_candidate"
      echo "==> Removed CLI symlink: $link_candidate"
      ;;
  esac
}

run_uninstall() {
  if [ -n "$INSTALL_DIR" ]; then
    if [ "$PLATFORM" = "darwin" ]; then
      target_app="$INSTALL_DIR/Hoxi.app"
      [ -d "$target_app" ] && rm -rf "$target_app" && echo "==> Removed $target_app"
    else
      [ -d "$INSTALL_DIR" ] && rm -rf "$INSTALL_DIR" && echo "==> Removed $INSTALL_DIR"
    fi
  else
    if [ "$PLATFORM" = "darwin" ]; then
      for app_cand in "$SYS_APPS_DIR/Hoxi.app" "$HOME_DIR/Applications/Hoxi.app"; do
        if [ -d "$app_cand" ] && [ -w "$(dirname "$app_cand")" ]; then
          rm -rf "$app_cand"
          echo "==> Removed $app_cand"
        fi
      done
    else
      if [ -d "$XDG_DATA/hoxi" ]; then
        rm -rf "$XDG_DATA/hoxi"
        echo "==> Removed $XDG_DATA/hoxi"
      fi
    fi
  fi

  if [ "$PLATFORM" = "linux" ]; then
    rm -f "$XDG_DATA/applications/hoxi.desktop"
    rm -f "$XDG_DATA/icons/hicolor/256x256/apps/hoxi.png"
  fi

  if [ -n "$BIN_DIR" ]; then
    remove_managed_symlink "$BIN_DIR/hoxi"
  else
    for dir_cand in "$USR_LOCAL_BIN" "$OPT_BREW_BIN" "$HOME_DIR/.local/bin" "${HOXI_HOME:-$HOME_DIR/.hoxi}/bin"; do
      remove_managed_symlink "$dir_cand/hoxi"
    done
  fi

  hoxi_state_dir="${HOXI_HOME:-$HOME_DIR/.hoxi}"
  if [ "$PURGE" -eq 1 ]; then
    if [ -d "$hoxi_state_dir" ]; then
      rm -rf "$hoxi_state_dir"
      echo "==> Purged user data directory: $hoxi_state_dir"
    fi
  elif [ -d "$hoxi_state_dir" ]; then
    echo "==> Kept user sessions & settings in $hoxi_state_dir (pass --purge to remove)"
  fi
  echo "==> Hoxi uninstall complete."
  exit 0
}

if [ "$UNINSTALL" -eq 1 ]; then
  run_uninstall
fi

CURL_BIN="${HOXI_INSTALL_CURL_BIN:-curl}"
command -v "$CURL_BIN" >/dev/null 2>&1 || { echo "Error: 'curl' is required to download Hoxi" >&2; exit 1; }

validate_semver() {
  ver_in="$(printf '%s' "$1" | sed 's/^[vV]//')"
  case "$ver_in" in
    ''|*[!0-9.]*|.*|*.|*..* )
      echo "Error: invalid release version '$1' (expected numeric major.minor.patch)" >&2
      exit 1
      ;;
  esac
  set -- $(printf '%s' "$ver_in" | tr '.' ' ')
  if [ $# -ne 3 ]; then
    echo "Error: invalid release version '$1' (expected major.minor.patch)" >&2
    exit 1
  fi
  printf '%s\n' "$ver_in"
}

fetch_file() {
  url="$1"
  out="$2"
  proto_policy="=https"
  if [ "${HOXI_INSTALL_TEST_FILE_URL:-0}" = "1" ]; then
    proto_policy="=https,file"
  fi
  if [ -n "$PROXY_URL" ]; then
    "$CURL_BIN" --fail --location --silent --show-error --proto "$proto_policy" --proto-redir "$proto_policy" --proxy "$PROXY_URL" "$url" -o "$out"
  else
    "$CURL_BIN" --fail --location --silent --show-error --proto "$proto_policy" --proto-redir "$proto_policy" "$url" -o "$out"
  fi
}

compute_sha256() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print tolower($1)}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print tolower($NF)}'
  else
    echo "Error: neither sha256sum, shasum, nor openssl is available to verify SHA-256" >&2
    exit 1
  fi
}

REPO="${HOXI_INSTALL_REPO:-hoxiai/hoxi}"
API_BASE="${HOXI_INSTALL_API_BASE:-https://api.github.com}"
RELEASE_BASE="${HOXI_INSTALL_RELEASE_BASE:-https://github.com/$REPO/releases/download}"

TMP_WORK="$(mktemp -d "${TMPDIR:-/tmp}/hoxi-install.XXXXXX")"
cleanup() {
  rm -rf "$TMP_WORK"
}
trap cleanup EXIT INT TERM

if [ -n "$VERSION" ]; then
  CLEAN_VER="$(validate_semver "$VERSION")"
else
  echo "==> Resolving latest Hoxi release version..."
  META_JSON="$TMP_WORK/release.json"
  fetch_file "$API_BASE/repos/$REPO/releases/latest" "$META_JSON"
  RAW_TAG="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$META_JSON" | head -n 1)"
  if [ -z "$RAW_TAG" ]; then
    echo "Error: unable to determine latest version from $API_BASE/repos/$REPO/releases/latest" >&2
    exit 1
  fi
  CLEAN_VER="$(validate_semver "$RAW_TAG")"
fi

if [ "$PLATFORM" = "darwin" ]; then
  ASSET_NAME="Hoxi-${CLEAN_VER}-darwin-${ARCH}.dmg"
else
  ASSET_NAME="Hoxi-${CLEAN_VER}-linux-${ARCH}.tar.gz"
fi

OFFICIAL_ASSET_URL="${RELEASE_BASE%/}/v${CLEAN_VER}/${ASSET_NAME}"
OFFICIAL_SHA_URL="${OFFICIAL_ASSET_URL}.sha256"

# Security invariant: SHA-256 sidecar is ALWAYS fetched from the official origin,
# never through a user-supplied third-party download mirror.
echo "==> Fetching official SHA-256 checksum for v${CLEAN_VER}..."
SHA_FILE="$TMP_WORK/${ASSET_NAME}.sha256"
fetch_file "$OFFICIAL_SHA_URL" "$SHA_FILE"
EXPECTED_SHA="$(tr -d '\r' < "$SHA_FILE" | awk '{print tolower($1)}' | head -n 1)"
case "$EXPECTED_SHA" in
  [0-9a-f][0-9a-f][0-9a-f][0-9a-f]*)
    if [ "${#EXPECTED_SHA}" -ne 64 ] || printf '%s' "$EXPECTED_SHA" | grep -q '[^0-9a-f]'; then
      echo "Error: malformed SHA-256 digest in $OFFICIAL_SHA_URL" >&2
      exit 1
    fi
    ;;
  *)
    echo "Error: missing or invalid SHA-256 digest in $OFFICIAL_SHA_URL" >&2
    exit 1
    ;;
esac

build_mirror_url() {
  prefix="${1%/}"
  case "$prefix" in
    file://*)
      printf '%s/v%s/%s\n' "$prefix" "$CLEAN_VER" "$ASSET_NAME"
      ;;
    http://*|https://*)
      host_rest="${prefix#*://}"
      case "$host_rest" in
        */*) printf '%s/v%s/%s\n' "$prefix" "$CLEAN_VER" "$ASSET_NAME" ;;
        *)   printf '%s/%s\n' "$prefix" "$OFFICIAL_ASSET_URL" ;;
      esac
      ;;
    *)
      printf '%s/%s\n' "$prefix" "$OFFICIAL_ASSET_URL"
      ;;
  esac
}

ARCHIVE_FILE="$TMP_WORK/$ASSET_NAME"
DOWNLOADED=0
if [ -n "$MIRROR_PREFIX" ]; then
  MIRROR_URL="$(build_mirror_url "$MIRROR_PREFIX")"
  echo "==> Downloading $ASSET_NAME via mirror..."
  if fetch_file "$MIRROR_URL" "$ARCHIVE_FILE"; then
    DOWNLOADED=1
  else
    echo "==> Mirror download failed; falling back to official release URL..." >&2
  fi
fi

if [ "$DOWNLOADED" -eq 0 ]; then
  echo "==> Downloading $ASSET_NAME..."
  fetch_file "$OFFICIAL_ASSET_URL" "$ARCHIVE_FILE"
fi

ACTUAL_SHA="$(compute_sha256 "$ARCHIVE_FILE")"
if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
  echo "Error: SHA-256 checksum mismatch for $ASSET_NAME!" >&2
  echo "  Expected (official): $EXPECTED_SHA" >&2
  echo "  Actual (downloaded): $ACTUAL_SHA" >&2
  exit 1
fi
echo "==> Verified SHA-256: $ACTUAL_SHA"

TARGET_INSTALL_DIR="$(resolve_default_install_dir)"
TARGET_BIN_DIR="$(resolve_default_bin_dir)"

if [ "$PLATFORM" = "darwin" ]; then
  MOUNT_DIR="$TMP_WORK/mnt"
  mkdir -p "$MOUNT_DIR"
  hdiutil attach -nobrowse -readonly -noverify -mountpoint "$MOUNT_DIR" "$ARCHIVE_FILE" >/dev/null
  detach_dmg() {
    hdiutil detach "$MOUNT_DIR" -force >/dev/null 2>&1 || true
  }
  if [ ! -x "$MOUNT_DIR/Hoxi.app/Contents/MacOS/Hoxi" ]; then
    detach_dmg
    echo "Error: mounted DMG does not contain Hoxi.app" >&2
    exit 1
  fi
  codesign --verify --deep --strict "$MOUNT_DIR/Hoxi.app" || {
    detach_dmg
    echo "Error: code signature verification failed for Hoxi.app" >&2
    exit 1
  }
  mkdir -p "$TARGET_INSTALL_DIR"
  DEST_APP="$TARGET_INSTALL_DIR/Hoxi.app"
  rm -rf "$DEST_APP"
  ditto "$MOUNT_DIR/Hoxi.app" "$DEST_APP"
  detach_dmg
  xattr -dr com.apple.quarantine "$DEST_APP" 2>/dev/null || true
  INSTALLED_EXE="$DEST_APP/Contents/MacOS/Hoxi"
  echo "==> Installed Hoxi.app v${CLEAN_VER} to $DEST_APP"
else
  EXTRACT_DIR="$TMP_WORK/extracted"
  mkdir -p "$EXTRACT_DIR"
  tar -xzf "$ARCHIVE_FILE" -C "$EXTRACT_DIR"
  if [ ! -f "$EXTRACT_DIR/Hoxi" ]; then
    echo "Error: Linux archive did not contain Hoxi binary" >&2
    exit 1
  fi
  mkdir -p "$TARGET_INSTALL_DIR"
  cp -f "$EXTRACT_DIR/Hoxi" "$TARGET_INSTALL_DIR/Hoxi.tmp.$$"
  chmod 0755 "$TARGET_INSTALL_DIR/Hoxi.tmp.$$"
  mv -f "$TARGET_INSTALL_DIR/Hoxi.tmp.$$" "$TARGET_INSTALL_DIR/Hoxi"
  INSTALLED_EXE="$TARGET_INSTALL_DIR/Hoxi"

  if [ -f "$EXTRACT_DIR/hoxi.png" ]; then
    cp -f "$EXTRACT_DIR/hoxi.png" "$TARGET_INSTALL_DIR/hoxi.png"
  fi
  if [ -f "$EXTRACT_DIR/hoxi.desktop" ]; then
    cp -f "$EXTRACT_DIR/hoxi.desktop" "$TARGET_INSTALL_DIR/hoxi.desktop"
  fi

  if [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; then
    if [ -f "$EXTRACT_DIR/hoxi.png" ]; then
      ICON_DIR="$XDG_DATA/icons/hicolor/256x256/apps"
      mkdir -p "$ICON_DIR"
      cp -f "$EXTRACT_DIR/hoxi.png" "$ICON_DIR/hoxi.png"
    fi
    if [ -f "$EXTRACT_DIR/hoxi.desktop" ]; then
      APP_MENU_DIR="$XDG_DATA/applications"
      mkdir -p "$APP_MENU_DIR"
      sed "s|^Exec=.*|Exec=\"$INSTALLED_EXE\" %U|" "$EXTRACT_DIR/hoxi.desktop" > "$APP_MENU_DIR/hoxi.desktop"
      chmod 0644 "$APP_MENU_DIR/hoxi.desktop"
    fi
    if command -v ldconfig >/dev/null 2>&1; then
      if ! ldconfig -p 2>/dev/null | grep -q 'libwebkit2gtk-4\.[01]'; then
        echo "==> Note: desktop GUI requires GTK3 + WebKit2GTK 4.1 (e.g. libwebkit2gtk-4.1-0 on Debian/Ubuntu)."
      fi
    fi
  fi
  echo "==> Installed Hoxi v${CLEAN_VER} to $TARGET_INSTALL_DIR"
fi

if [ "$INSTALL_CLI" -eq 1 ]; then
  mkdir -p "$TARGET_BIN_DIR"
  LINK_PATH="$TARGET_BIN_DIR/hoxi"
  if [ -e "$LINK_PATH" ] && [ ! -L "$LINK_PATH" ]; then
    echo "==> Skipping CLI symlink: $LINK_PATH already exists as a regular file" >&2
  else
    TMP_LINK="$TARGET_BIN_DIR/.hoxi-link.$$"
    rm -f "$TMP_LINK"
    ln -s "$INSTALLED_EXE" "$TMP_LINK"
    mv -f "$TMP_LINK" "$LINK_PATH"
    echo "==> Linked CLI: $LINK_PATH -> $INSTALLED_EXE"
    case ":${PATH:-}:" in
      *":$TARGET_BIN_DIR:"*) ;;
      *)
        if [ "$NO_LAUNCH_HINT" -eq 0 ]; then
          echo "==> Add '$TARGET_BIN_DIR' to your shell profile PATH to invoke 'hoxi' directly."
        fi
        ;;
    esac
  fi
fi

if [ "$NO_LAUNCH_HINT" -eq 0 ] && [ "$PLATFORM" = "linux" ] && [ -z "${DISPLAY:-}" ] && [ -z "${WAYLAND_DISPLAY:-}" ]; then
  echo "==> Headless Linux detected. Start a remote development host with:"
  echo "      $INSTALLED_EXE host start"
fi
