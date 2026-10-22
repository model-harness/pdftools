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
# By default the result is $ROOT/install: bin/tesseract, lib/ and include/ for linking against
# libtesseract, and share/tessdata/eng.traineddata. The binary finds its libraries relative to
# itself, so the install can be moved.
#
# With --portable it is the Tesseract pdfspec ships instead, $ROOT/portable/tesseract: one
# tesseract with zlib and libpng linked in, and on Linux libstdc++ too, so it needs nothing but the
# C library a system has, beside tessdata/eng.traineddata and licenses/.
#
# Usage: native/tesseract/build.sh [--clean] [--portable]
#   TESS_BUILD_ROOT  where sources, build trees and results go (default: build/native, or
#                    build/portable with --portable, at the repository root)
# Needs: cmake, a C and C++ compiler, curl, tar, and ninja or make.
set -eu

CLEAN=0
PORTABLE=0
for arg in "$@"; do
    case "$arg" in
        --clean) CLEAN=1 ;;
        --portable) PORTABLE=1 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

HERE=$(cd "$(dirname "$0")" && pwd)
if [ "$PORTABLE" = 1 ]; then default=build/portable; else default=build/native; fi
ROOT="${TESS_BUILD_ROOT:-$(cd "$HERE/../.." && pwd)/$default}"
SRC="$ROOT/src"
BUILD="$ROOT/build"
INSTALL="$ROOT/install"

if [ "$CLEAN" = 1 ]; then
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

# Shared zlib and libpng for the linking build; static alone for the portable one, named outright
# so that leptonica and tesseract cannot pick up a system copy instead.
if [ "$PORTABLE" = 1 ]; then
    SHARED=OFF
    set -- -DZLIB_LIBRARY="$INSTALL/lib/libz.a" -DZLIB_INCLUDE_DIR="$INSTALL/include"
else
    SHARED=ON
    set --
fi
# macOS 11 is the oldest a portable build runs on: what it links is the system's own libraries.
if [ "$(uname -s)" = Darwin ]; then
    set -- "$@" -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0
fi
HINTS=$(printf '%s\n' "$@")

# build NAME TREE OPTION... configures, builds and installs one CMake project into $INSTALL.
build() {
    name="$1"; tree="$2"; shift 2
    # The hints go first, one per line, so a path with a space would still be one argument.
    old_ifs=$IFS
    IFS='
'
    # shellcheck disable=SC2086
    set -- $HINTS "$@"
    IFS=$old_ifs
    echo "==> Configuring $name"
    cmake -S "$tree" -B "$BUILD/$name" -G "$GENERATOR" \
        -DCMAKE_INSTALL_PREFIX="$INSTALL" \
        -DCMAKE_PREFIX_PATH="$INSTALL" \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_LIBDIR=lib \
        -DCMAKE_INSTALL_RPATH="$ORIGIN/../lib" \
        -DCMAKE_INSTALL_NAME_DIR=@rpath \
        -DBUILD_SHARED_LIBS=OFF \
        "$@"
    echo "==> Building $name"
    cmake --build "$BUILD/$name" --config Release --parallel
    cmake --install "$BUILD/$name" --config Release
}

build zlib "$(source_tree zlib)" \
    -DZLIB_BUILD_EXAMPLES=OFF \
    -DZLIB_BUILD_TESTING=OFF \
    -DZLIB_BUILD_SHARED="$SHARED" \
    -DZLIB_BUILD_STATIC=ON

if [ "$PORTABLE" = 1 ]; then PNG_STATIC=ON; else PNG_STATIC=OFF; fi
build libpng "$(source_tree libpng)" \
    -DPNG_SHARED="$SHARED" \
    -DPNG_STATIC="$PNG_STATIC" \
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

# The portable Linux binary carries libstdc++ and libgcc, whose versions differ between
# distributions far more than the C library's do.
LINK=""
if [ "$PORTABLE" = 1 ] && [ "$(uname -s)" != Darwin ]; then
    LINK="-static-libstdc++ -static-libgcc"
fi

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
    -DCMAKE_EXE_LINKER_FLAGS="$LINK" \
    -DLeptonica_DIR="$INSTALL/lib/cmake/leptonica"

TESSDATA="$INSTALL/share/tessdata"
mkdir -p "$TESSDATA"
fetch eng "$TESSDATA" >/dev/null

echo "==> Verifying the result"
EXE="$INSTALL/bin/tesseract"
case "$(uname -s)" in
    Darwin) deps=$(otool -L "$EXE" | tail -n +2 | awk '{print $1}') ;;
    *)      deps=$(ldd "$EXE" | awk '{print $1}') ;;
esac
echo "Linked (must not name libcurl or archive):"
echo "$deps" | sed 's/^/  /'
if echo "$deps" | grep -Eq 'curl|archive'; then
    echo "  FAIL: libcurl or libarchive is linked" >&2
    exit 1
fi
if [ "$PORTABLE" = 1 ]; then
    # What any system has: the C library and the loader on Linux, the system's own libraries on
    # macOS. Anything else is a library the machine it lands on may not have.
    foreign=$(echo "$deps" | grep -Ev '^(linux-vdso\.so\.1|/lib.*/ld-linux.*|ld-linux.*|lib(c|m|pthread|dl|rt)\.so\.[0-9]+|/usr/lib/libSystem\.B\.dylib|/usr/lib/libc\+\+\.1\.dylib)$' || true)
    if [ -n "$foreign" ]; then
        echo "  FAIL: the portable tesseract links $foreign, which a machine may not have" >&2
        exit 1
    fi
fi
# Tesseract's version lists the libraries compiled in, static ones included, which ldd cannot.
version=$("$EXE" --version 2>&1)
echo "$version" | sed 's/^/  /'
if echo "$version" | grep -Eq 'curl|archive'; then
    echo "  FAIL: tesseract reports libcurl or libarchive" >&2
    exit 1
fi
echo "  OK"

if [ "$PORTABLE" = 0 ]; then
    echo ""
    echo "Install prefix: $INSTALL"
    echo ""
    echo "For pdfspec:"
    echo "  export PDFSPEC_TESSERACT='$EXE'"
    echo "  export TESSDATA_PREFIX='$TESSDATA'"
    exit 0
fi

# What pdfspec ships beside itself: the executable, the model, and the licences of everything in
# them, which their terms require a copy to carry.
OUT="$ROOT/portable/tesseract"
echo "==> Laying out $OUT"
rm -rf "$OUT"
mkdir -p "$OUT/tessdata" "$OUT/licenses"
cp "$EXE" "$OUT/tesseract"
cp "$TESSDATA/eng.traineddata" "$OUT/tessdata/"
cp "$SRC/tesseract-$(pin tesseract | cut -d' ' -f1)/LICENSE" "$OUT/licenses/tesseract.txt"
cp "$SRC/leptonica-$(pin leptonica | cut -d' ' -f1)/leptonica-license.txt" "$OUT/licenses/leptonica.txt"
cp "$SRC/libpng-$(pin libpng | cut -d' ' -f1)/LICENSE" "$OUT/licenses/libpng.txt"
cp "$SRC/zlib-$(pin zlib | cut -d' ' -f1)/LICENSE" "$OUT/licenses/zlib.txt"
cp "$(fetch eng-license "$SRC")" "$OUT/licenses/tessdata_fast.txt"

# It must read from where it stands, with its own model and nothing else.
if ! "$OUT/tesseract" --tessdata-dir "$OUT/tessdata" --list-langs 2>&1 | grep -qx eng; then
    echo "the portable tesseract does not list eng" >&2
    exit 1
fi
echo ""
echo "Portable Tesseract: $OUT"
(cd "$OUT" && find . -type f -exec ls -l {} + | awk '{printf "  %-40s %8.2f MB\n", $9, $5/1048576}')
