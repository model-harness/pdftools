# 50. Tesseract is built here

Date: 2026-10-22

## Status

Accepted.

## Context

`pdfspec` reads scans and glyphs with no value through Tesseract when one is installed (ADRs 0043, 0047 and 0048). The Tesseract it was measured with was built by print2, a virtual printer, whose `build/build-tesseract.ps1` and `build/build-tesseract-mac.sh` build Tesseract 5.5.3 from source with the network and archive libraries left out. Nothing in this repository could build it, so CI never ran `TestBenchmarkWithTesseract` and the seven fixtures that need it were measured on one machine. print2 is moving to this library, so the engine's build should move with it.

Moving the scripts showed four defects in them:

- **Nothing checked a download.** A gzip header was the whole test, so a changed archive, or the English model fetched from tessdata_fast's `main`, would have been built from silently.
- **The Windows script failed without Ninja**, falling back to the Visual Studio 2022 generator by name on a machine with only Visual Studio 2026.
- **Run from Git Bash, it unpacked nothing:** PATH found GNU tar, which reads `C:` as a remote host.
- **Its check for curl never ran.** It looked for a Tesseract DLL, and libtesseract builds as a static library inside `tesseract.exe`, so the check was skipped without a word.

## Decision

**`native/tesseract/` builds Tesseract,** with `build.ps1` for Windows and `build.sh` for Linux and macOS. It builds the same configuration as print2: no curl, no libarchive, no tiff, no legacy engine, no training tools, and a Leptonica that reads only PNG. The result goes in `build/native/install`, which is gitignored: `bin/tesseract`, the headers and libraries for linking against libtesseract, and `share/tessdata/eng.traineddata`.

**`versions.txt` pins every input** by version and SHA-256, the four source archives and the English model, which is pinned to the tessdata_fast commit that last changed it. Both scripts read the file, and check each download's hash before anything reads it. libpng moves from 1.6.58 to 1.6.59, released on 2026-09-28, which clears stale stream pointers when an image's compressed data is released.

**zlib and libpng are built on every platform,** where print2's macOS script took the system's and Homebrew's. That way all three platforms link the versions the file pins. On Linux and macOS the binary finds its libraries relative to itself, so the install can be moved.

**Each script proves what it built.** It checks the binary's imports, and then the libraries `tesseract --version` reports, since that lists the static ones too. The build fails if either names curl or libarchive.

**CI builds it on Linux, Windows and macOS** in a `tesseract` job, caching the install on the scripts and their pins, and runs `./ocr/tesseract` and `./cmd/pdfspec` with `PDFSPEC_TEST_TESSERACT` set. The benchmark's every target is then held over all 33 fixtures, scans included, on every push.

## Acceptance

- On Windows, with Visual Studio 2026 and no Ninja, `build.ps1` builds in 133 seconds, then 28 seconds incrementally. `tesseract --version` reports leptonica 1.87.0, libpng 1.6.59 and zlib 1.3.2, and nothing else.
- On Ubuntu 24.04, `build.sh` builds in 99 seconds with make. The binary loads its own libpng and zlib through `$ORIGIN/../lib`, and still runs after its install is moved.
- The check has teeth. Ubuntu's own Tesseract, 5.3.4, reports `Found libarchive 3.7.2` and `Found libcurl/8.5.0` in its version, which fails it.
- Every archive the two repositories share hashes byte for byte as print2's copies, downloaded in August. So does the English model at its pinned commit, against print2's copy from `main`.
- With this build, `TestBenchmarkWithTesseract` gives means identical to print2's build in every digit, over the 33 fixtures: CER 0.0030, recall 0.995, precision 0.998, order 1.000, blocks 0.968, headings 0.978, tables 0.959 and style 0.967.

## Consequences

- print2 can build its engine from this repository and link libtesseract from `build/native/install`.
- A version bump is one line in `versions.txt` with its new hash, and CI rebuilds and re-measures on it.
- The README's install notes for Tesseract name the scripts beside the package managers. A distribution's Tesseract still works, at whatever version it ships.
