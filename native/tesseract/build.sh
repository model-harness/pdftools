#!/bin/sh
# Builds Tesseract and its minimal dependency chain from source on Linux and macOS, with the same
# trimmed configuration as build.ps1: no curl, no libarchive, no tiff, no legacy engine, and a
# Leptonica that reads PNG only. An OCR engine reading a page image has no business with a network
# stack, and every library linked in is CVE surface to patch.
#
# zlib and libpng are built here too, rather than taken from the system or Homebrew, so that every
# platform links the versions versions.txt pins, and each archive is checked against its SHA-256
# before anything reads it.
#
# The result is $ROOT/install: bin/tesseract, lib/ and include/ for linking against libtesseract,
# and share/tessdata/eng.traineddata. The binary finds its libraries relative to itself, so the
# install can be moved.
#
# Usage: native/tesseract/build.sh [--clean]
#   TESS_BUILD_ROOT  where sources, build trees and install go (default: build/native at the
#                    repository root)
# Needs: cmake, a C and C++ compiler, curl, tar, and ninja or make.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT="${TESS_BUILD_ROOT:-$(cd "$HERE/../.." && pwd)/build/native}"
SRC="$ROOT/src"
BUILD="$ROOT/build"
INSTALL="$ROOT/install"

if [ "${1:-}" = "--clean" ]; then
    rm -rf "$ROOT"
fi
mkdir -p "$SRC" "$BUILD" "$INSTALL"

# The literal $ORIGIN is for the loader, not the shell.
# shellcheck disable=SC2016
case "$(uname -s)" in
    Darwin) ORIGIN='@loader_path'; sha256() { shasum -a 256 "$1" | cut -d' ' -f1; } ;;
    *)      ORIGIN='$ORIGIN';      sha256() { sha256sum "$1" | cut -d' ' -f1; } ;;
esac

# pin NAME prints NAME's version, hash and URL from versions.txt, the URL with {v} replaced.
pin() {
    awk -v n="$1" '$1 == n { u = $4; gsub(/\{v\}/, $2, u); print $2, $3, u }' "$HERE/versions.txt"
}

# fetch NAME DIR downloads NAME's pin into DIR, checks its SHA-256, and prints the file's path.
fetch() {
    # Split on purpose: the pin is three words.
    # shellcheck disable=SC2046
    set -- "$1" "$2" $(pin "$1")
    [ $# -eq 5 ] || { echo "no pin for $1 in versions.txt" >&2; exit 1; }
    file="$2/$(basename "$5")"
    if [ ! -f "$file" ]; then
        echo "==> Downloading $1 $3" >&2
        curl -fsSL -o "$file" "$5"
    fi
    got=$(sha256 "$file")
    if [ "$got" != "$4" ]; then
        rm -f "$file"
        echo "$1 download has SHA-256 $got, want $4. Check versions.txt." >&2
        exit 1
    fi
    echo "$file"
}

# source NAME unpacks NAME's archive into $SRC and prints the tree, NAME-VERSION.
source_tree() {
    archive=$(fetch "$1" "$SRC")
    tree="$SRC/$1-$(pin "$1" | cut -d' ' -f1)"
    if [ ! -d "$tree" ]; then
        echo "==> Unpacking $1" >&2
        tar -xf "$archive" -C "$SRC"
    fi
    [ -d "$tree" ] || { echo "unpacked $1 but $tree is missing" >&2; exit 1; }
    echo "$tree"
}

GENERATOR="Unix Makefiles"
command -v ninja >/dev/null 2>&1 && GENERATOR=Ninja

# build NAME TREE OPTION... configures, builds and installs one CMake project into $INSTALL.
build() {
    name="$1"; tree="$2"; shift 2
    echo "==> Configuring $name"
    cmake -S "$tree" -B "$BUILD/$name" -G "$GENERATOR" \
        -DCMAKE_INSTALL_PREFIX="$INSTALL" \
        -DCMAKE_PREFIX_PATH="$INSTALL" \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_LIBDIR=lib \
        -DCMAKE_INSTALL_RPATH="$ORIGIN/../lib" \
        -DCMAKE_INSTALL_NAME_DIR=@rpath \
        "$@"
    echo "==> Building $name"
    cmake --build "$BUILD/$name" --config Release --parallel
    cmake --install "$BUILD/$name" --config Release
}

build zlib "$(source_tree zlib)" \
    -DZLIB_BUILD_EXAMPLES=OFF

build libpng "$(source_tree libpng)" \
    -DPNG_SHARED=ON \
    -DPNG_STATIC=OFF \
    -DPNG_TESTS=OFF \
    -DPNG_TOOLS=OFF \
    -DPNG_FRAMEWORK=OFF \
    -DZLIB_ROOT="$INSTALL"

# Only PNG: the only image Leptonica is handed is a PNG of a rendered page.
build leptonica "$(source_tree leptonica)" \
    -DBUILD_PROG=OFF \
    -DSW_BUILD=OFF \
    -DENABLE_ZLIB=ON \
    -DENABLE_PNG=ON \
    -DENABLE_GIF=OFF \
    -DENABLE_JPEG=OFF \
    -DENABLE_TIFF=OFF \
    -DENABLE_WEBP=OFF \
    -DENABLE_OPENJPEG=OFF

# The disables are the point; DISABLE_CURL is the one this script exists for. The legacy engine
# is the pre-4.0 pattern matcher, and LSTM is what is used.
build tesseract "$(source_tree tesseract)" \
    -DDISABLE_CURL=ON \
    -DDISABLE_ARCHIVE=ON \
    -DDISABLE_TIFF=ON \
    -DBUILD_TRAINING_TOOLS=OFF \
    -DBUILD_TESTS=OFF \
    -DGRAPHICS_DISABLED=ON \
    -DDISABLED_LEGACY_ENGINE=ON \
    -DOPENMP_BUILD=OFF \
    -DLeptonica_DIR="$INSTALL/lib/cmake/leptonica"

TESSDATA="$INSTALL/share/tessdata"
mkdir -p "$TESSDATA"
fetch eng "$TESSDATA" >/dev/null

echo "==> Verifying the result"
EXE="$INSTALL/bin/tesseract"
echo "Dependency check (must not name libcurl or archive):"
# The literal $ORIGIN is for the loader, not the shell.
# shellcheck disable=SC2016
case "$(uname -s)" in
    Darwin) deps=$(otool -L "$EXE") ;;
    *)      deps=$(ldd "$EXE") ;;
esac
echo "$deps" | sed 's/^/  /'
if echo "$deps" | grep -Eq 'curl|archive'; then
    echo "  FAIL: libcurl or libarchive is linked" >&2
    exit 1
fi
# Tesseract's version lists the libraries compiled in, static ones included, which ldd cannot.
version=$("$EXE" --version 2>&1)
echo "$version" | sed 's/^/  /'
if echo "$version" | grep -Eq 'curl|archive'; then
    echo "  FAIL: tesseract reports libcurl or libarchive" >&2
    exit 1
fi
echo "  OK: no curl, no archive"

echo ""
echo "Install prefix: $INSTALL"
echo ""
echo "For pdfspec:"
echo "  export PDFSPEC_TESSERACT='$EXE'"
echo "  export TESSDATA_PREFIX='$TESSDATA'"
