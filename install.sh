#!/bin/sh
# Installs pdfspec on Linux or macOS from its GitHub release: the build for this machine, checked
# against the release's checksums before it is installed, with the Tesseract it ships for scans.
#
#   curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | sh
#
# PDFSPEC_VERSION picks a release, such as v1.0.0 (default: the latest), and PDFSPEC_INSTALL_DIR
# where the binary goes (default: ~/.local/bin). Tesseract goes beside it, in ../share/pdfspec.
# PDFSPEC_DOWNLOAD_URL takes the release's files from a mirror of them instead of GitHub.
#
# PDFSPEC_WITH_INFERD=1 also installs inferd (https://inferd.io), the local model daemon pdfspec
# asks to describe pictures and charts. It runs as your own user service and, on first start,
# downloads its model, about 6 GB. Without it pdfspec reads everything else.
#
#   curl -fsSL https://raw.githubusercontent.com/model-harness/pdftools/main/install.sh | PDFSPEC_WITH_INFERD=1 sh
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
if [ -n "${PDFSPEC_DOWNLOAD_URL:-}" ]; then
  base=$PDFSPEC_DOWNLOAD_URL
elif [ "$version" = latest ]; then
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
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz"
fetch "$base/checksums.txt" "$tmp/checksums.txt"
# sha256sum marks a name read in binary mode with "*", as it does on Windows.
want=$(grep -E " \*?$name\.tar\.gz\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)
if [ -z "$want" ] || [ "$(sha256 "$tmp/$name.tar.gz")" != "$want" ]; then
  echo "pdfspec: $name.tar.gz does not match the release's checksum; nothing was installed." >&2
  exit 1
fi

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dir"
cp "$tmp/$name/pdfspec" "$dir/pdfspec"
chmod 0755 "$dir/pdfspec"
# Where pdfspec looks for the Tesseract it ships: ../share/pdfspec from its own directory. A
# release from before Tesseract was bundled has none, and pdfspec then looks on PATH.
if [ -d "$tmp/$name/tesseract" ]; then
  share="$(cd "$dir/.." && pwd)/share/pdfspec"
  mkdir -p "$share"
  rm -rf "$share/tesseract"
  cp -R "$tmp/$name/tesseract" "$share/tesseract"
  chmod 0755 "$share/tesseract/tesseract"
fi
echo "installed $("$dir/pdfspec" version | head -n 1) to $dir/pdfspec"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH to run it as pdfspec" ;;
esac

[ "${PDFSPEC_WITH_INFERD:-}" = 1 ] || exit 0

# inferd, at the release pdfspec is tested with, checked against that release's hashes as pinned
# here, then installed by its own installer as https://inferd.io describes.
iv=v0.8.0
case "$os/$arch" in
  linux/amd64) target=x86_64-unknown-linux-gnu; isha=f4896a462e82c67606ae061001148f933b1ccb10a31083c996c367e7df8d859a ;;
  linux/arm64) target=aarch64-unknown-linux-gnu; isha=25abb893a22d996863f15333afb1fd900d4cb727e327eae2afc9b5b9b7d1925c ;;
  darwin/arm64) target=aarch64-apple-darwin; isha=8724b4d14df7811f0d49fb501a8abd8e65b6127822c1a4a03ed89f419c5cd6b4 ;;
  *) echo "inferd has no build for $os/$arch; pdfspec works without it, and leaves pictures undescribed." >&2; exit 0 ;;
esac
pkg="inferd-$iv-$target"
echo "downloading inferd $iv"
fetch "https://github.com/3rg0n/inferd/releases/download/$iv/$pkg.tar.gz" "$tmp/$pkg.tar.gz"
if [ "$(sha256 "$tmp/$pkg.tar.gz")" != "$isha" ]; then
  echo "inferd: $pkg.tar.gz does not match its pinned checksum; inferd was not installed." >&2
  exit 1
fi

if [ "$os" = darwin ]; then
  # On macOS the unpacked archive is the install: the LaunchAgent runs the daemon where it is.
  home="$HOME/.local/share/inferd"
  mkdir -p "$home"
  rm -rf "${home:?}/$pkg"
  tar -xzf "$tmp/$pkg.tar.gz" -C "$home"
  (cd "$home/$pkg" && ./packaging/install-launchagent.sh ./inferd-daemon)
  ctl="$home/$pkg/inferdctl"
else
  tar -xzf "$tmp/$pkg.tar.gz" -C "$tmp"
  cp -f "$tmp/$pkg/inferd-daemon" "$tmp/$pkg/inferdctl" "$tmp/$pkg"/backends/* "$dir/"
  ctl="$dir/inferdctl"
  if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
    mkdir -p "$HOME/.config/systemd/user"
    cp -f "$tmp/$pkg/packaging/inferd.service" "$HOME/.config/systemd/user/"
    systemctl --user daemon-reload
    systemctl --user enable --now inferd
  else
    echo "inferd: no systemd user session here, so it is not started as a service; run $dir/inferd-daemon yourself."
  fi
fi
echo "installed inferd $iv. On first start it downloads its model, about 6 GB; follow it with: $ctl watch"
