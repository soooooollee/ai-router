#!/bin/sh

set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t airoute-install-test)
trap 'rm -rf "$tmp"' EXIT INT TERM

release="$tmp/release"
payload="$tmp/payload"
install_dir="$tmp/bin"
mkdir -p "$release" "$payload"

cat >"$payload/airoute" <<'SCRIPT'
#!/bin/sh
printf 'airoute test-version\n'
SCRIPT
chmod 0755 "$payload/airoute"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) exit 0 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) exit 0 ;;
esac

asset="airoute_9.9.9_${os}_${arch}.tar.gz"
tar -czf "$release/$asset" -C "$payload" airoute
if command -v sha256sum >/dev/null 2>&1; then
  checksum=$(sha256sum "$release/$asset" | awk '{print $1}')
else
  checksum=$(shasum -a 256 "$release/$asset" | awk '{print $1}')
fi
printf '%s  %s\n' "$checksum" "$asset" >"$release/checksums.txt"

AIROUTE_VERSION=9.9.9 \
AIROUTE_DOWNLOAD_BASE="file://$release" \
AIROUTE_INSTALL_DIR="$install_dir" \
  sh "$root/install.sh" >/dev/null

test "$("$install_dir/airoute")" = "airoute test-version"
printf 'install.sh test passed\n'
