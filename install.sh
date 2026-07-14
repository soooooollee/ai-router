#!/bin/sh

set -eu

REPO=${AIROUTE_REPO:-soooooollee/ai-router}
INSTALL_DIR=${AIROUTE_INSTALL_DIR:-${XDG_BIN_DIR:-${HOME:-}/.local/bin}}
VERSION=${AIROUTE_VERSION:-}
GITHUB_TOKEN=${AIROUTE_GITHUB_TOKEN:-${GITHUB_TOKEN:-}}

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'airoute: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

download() {
  source_url=$1
  destination=$2
  if command_exists curl; then
    if [ -n "$GITHUB_TOKEN" ]; then
      curl -fsSL --retry 3 -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" "$source_url" -o "$destination"
    else
      curl -fsSL --retry 3 "$source_url" -o "$destination"
    fi
    return
  fi
  if command_exists wget; then
    if [ -n "$GITHUB_TOKEN" ]; then
      wget -q --header="Authorization: Bearer $GITHUB_TOKEN" --header="Accept: application/octet-stream" -O "$destination" "$source_url"
    else
      wget -q -O "$destination" "$source_url"
    fi
    return
  fi
  fail "curl or wget is required"
}

detect_platform() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    VERSION=${VERSION#v}
    return
  fi
  metadata="$tmp_dir/latest.json"
  download "https://api.github.com/repos/$REPO/releases/latest" "$metadata"
  VERSION=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' "$metadata" | head -n 1)
  [ -n "$VERSION" ] || fail "could not determine the latest release"
}

verify_checksum() {
  archive=$1
  checksum_file=$2
  archive_name=$3
  expected=$(awk -v name="$archive_name" '$2 == name || $2 == "*" name { print $1; exit }' "$checksum_file")
  [ -n "$expected" ] || fail "checksum for $archive_name was not found"

  if command_exists sha256sum; then
    actual=$(sha256sum "$archive" | awk '{print $1}')
  elif command_exists shasum; then
    actual=$(shasum -a 256 "$archive" | awk '{print $1}')
  else
    fail "sha256sum or shasum is required"
  fi
  [ "$actual" = "$expected" ] || fail "checksum verification failed for $archive_name"
}

[ -n "$INSTALL_DIR" ] || fail "HOME, XDG_BIN_DIR, or AIROUTE_INSTALL_DIR must be set"

umask 077
tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t airoute)
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

detect_platform
resolve_version

asset="airoute_${VERSION}_${os}_${arch}.tar.gz"
if [ -n "${AIROUTE_DOWNLOAD_BASE:-}" ]; then
  download_base=${AIROUTE_DOWNLOAD_BASE%/}
else
  download_base="https://github.com/$REPO/releases/download/v$VERSION"
fi

say "Installing AI Router v$VERSION for $os/$arch..."
if [ -n "$GITHUB_TOKEN" ] && [ -z "${AIROUTE_DOWNLOAD_BASE:-}" ] && command_exists gh; then
  GH_TOKEN=$GITHUB_TOKEN gh release download "v$VERSION" --repo "$REPO" --pattern "$asset" --pattern checksums.txt --dir "$tmp_dir"
else
  download "$download_base/$asset" "$tmp_dir/$asset"
  download "$download_base/checksums.txt" "$tmp_dir/checksums.txt"
fi
verify_checksum "$tmp_dir/$asset" "$tmp_dir/checksums.txt" "$asset"

tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
binary=$(find "$tmp_dir" -type f -name airoute -perm -u+x | head -n 1)
[ -n "$binary" ] || fail "the release archive did not contain the airoute binary"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$binary" "$INSTALL_DIR/airoute"

say "Installed airoute to $INSTALL_DIR/airoute"
case ":${PATH:-}:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Add $INSTALL_DIR to PATH to run airoute from any shell." ;;
esac
say "Run 'airoute version' to verify the installation."
