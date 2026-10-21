#!/bin/sh
# Installs pdfspec on Linux or macOS from its GitHub release: the build for this machine, checked
# against the release's checksums before it is installed.
#
#   curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | sh
#
# PDFSPEC_VERSION picks a release, such as v1.0.0 (default: the latest), and PDFSPEC_INSTALL_DIR
# where the binary goes (default: ~/.local/bin).
set -eu

repo=model-harness/pdftools
version=${PDFSPEC_VERSION:-latest}
dir=${PDFSPEC_INSTALL_DIR:-$HOME/.local/bin}

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "pdfspec: no build for $(uname -s). On Windows, run install.ps1." >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "pdfspec: no build for $(uname -m)." >&2; exit 1 ;;
esac

name="pdfspec_${os}_${arch}"
if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}
fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz"
fetch "$base/checksums.txt" "$tmp/checksums.txt"

want=$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name.tar.gz" | cut -d ' ' -f 1)
else
  got=$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d ' ' -f 1)
fi
if [ -z "$want" ] || [ "$got" != "$want" ]; then
  echo "pdfspec: $name.tar.gz does not match the release's checksum; nothing was installed." >&2
  exit 1
fi

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dir"
cp "$tmp/$name/pdfspec" "$dir/pdfspec"
chmod 0755 "$dir/pdfspec"
echo "installed $("$dir/pdfspec" version | head -n 1) to $dir/pdfspec"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH to run it as pdfspec" ;;
esac
