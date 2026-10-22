# 52. pdfspec ships its Tesseract

Date: 2026-10-23

## Status

Accepted. Supersedes issue #14, a separate repository for the Tesseract build, which ADR 0050 placed here instead.

## Context

Installing `pdfspec` gave a converter that read every computer-made PDF, and then asked the user to install Tesseract for scans: `apt install tesseract-ocr`, `brew install tesseract`, or a third-party installer on Windows. Whatever that gave them was not the build the gold set is measured with. Ubuntu's, for one, is Tesseract 5.3.4 with libcurl and libarchive linked in. A user who skipped the step got scans written as nothing, with a warning.

ADR 0050 made this repository build Tesseract 5.5.3 from pinned sources. That build is a set of libraries and an executable that need the libraries beside them, which suits print2 linking libtesseract, not a user unpacking an archive.

The second engine, a vision model through inferd, is different in kind. Its model is about 6 GB, and it is needed only for describing pictures and charts, and for a second opinion on doubtful words. That makes it an opt-in, not a default.

## Decision

**A portable Tesseract.**

- `native/tesseract/build.sh --portable` and `build.ps1 -Portable` build one executable with zlib and libpng linked in, on Windows the C runtime too, and on Linux libstdc++ and libgcc. A machine needs nothing installed to run it.
- Each script checks the result against an allowlist, and fails the build otherwise:
  - on Windows, the system's own DLLs, and in fact only `KERNEL32.dll`;
  - on Linux, the C library, `libm` and the loader;
  - on macOS, `libSystem` and `libc++`, with macOS 11 as the deployment target.
- The script lays out what pdfspec ships: `tesseract/tesseract`, `tesseract/tessdata/eng.traineddata`, and `tesseract/licenses/`. That last holds the licences of Tesseract, Leptonica, libpng, zlib and the tessdata_fast model, which their terms require a copy to carry.
- The model's licence is pinned in `versions.txt` too. It was added to tessdata_fast after the model's commit, so it has a commit of its own.
- The default build is unchanged, the libraries print2 links. Two fixes came out of building it statically:
  - a static zlib installs as `zs.lib` on Windows, so it is named to every later build;
  - Leptonica declares an old minimum CMake, which leaves policy CMP0091 off, and without it Leptonica ignored the runtime setting and built against the DLL C runtime. `CMAKE_POLICY_DEFAULT_CMP0091=NEW` turns it on.

**pdfspec asks Tesseract for TSV by setting, not by config file.** It passed `tsv`, a file in `tessdata/configs/`. A model directory holding only `eng.traineddata`, as the bundle does and as a clone of tessdata_fast does, has no such file, and Tesseract then writes plain text without a word of warning. The bundle's benchmark read CER 0.172 that way. `-c tessedit_create_tsv=1` asks for the same output with no file. `TestReadsWithAModelDirectoryAlone` failed before the change and passes after.

**pdfspec finds the Tesseract it ships.** With no `-tesseract` flag and no `PDFSPEC_TESSERACT`, the command looks in two places, before PATH:

- `tesseract/` beside itself, where the archive unpacks it;
- `../share/pdfspec/tesseract/` from its own directory, where `install.sh` puts it, beside `~/.local/bin` rather than in it.

That copy is the measured build. Its model is its own, whatever `TESSDATA_PREFIX` says, since that variable names the model of some other install. `probe` reports the same engine, since it resolves engines through the same path. The library, `pdfspec.Options`, still reads no environment and has no notion of a bundle: a program embedding the converter names its Tesseract itself.

**CI builds the whole product, and the release publishes what CI built.**

- `.github/workflows/build.yml` runs on each platform's own runner: `ubuntu-22.04` and `ubuntu-22.04-arm`, `macos-15-intel` and `macos-latest`, and `windows-latest` and `windows-11-arm`. On each, it:
  - builds the portable Tesseract, cached on `native/tesseract/**`;
  - builds pdfspec, and assembles the archive's directory;
  - runs the Tesseract tests on that Tesseract, failing if any skips for want of it, with `TestBenchmarkWithTesseract` required by name;
  - runs the assembled pdfspec as a user gets it, with nothing set and no PATH, and checks that `probe` names Tesseract 5 and that `md` reads the scan;
  - uploads the directory as an artifact.
- `ci.yml` calls it on every push. `release.yml` calls it at the tag, then packages those artifacts, checks every binary carries the tag, attests them and publishes them. The executable bit an artifact loses is restored before archiving.
- Linux builds on 22.04, so the one library the Tesseract links, the C library, needs glibc 2.35 or later.

**The installers install the whole product.** `install.sh` and `install.ps1` copy `tesseract/` to where pdfspec looks for it. A release from before this change has none, and they install pdfspec alone, as before. `PDFSPEC_DOWNLOAD_URL` takes the release's files from a mirror, which is also how the installers were tested before a release existed. The checksum line is matched with or without sha256sum's `*` binary marker.

**inferd is an opt-in at install.** `PDFSPEC_WITH_INFERD=1` also installs inferd v0.8.0, the release pdfspec is tested with:

- The archive is checked against its SHA-256, which the installers pin.
- inferd is installed by inferd's own installer, as https://inferd.io describes:
  - on Linux, its binaries beside pdfspec and its systemd user unit;
  - on macOS, the archive unpacked to `~/.local/share/inferd`, where its LaunchAgent runs it;
  - on Windows, its `packaging\install.ps1`, which stages it in `%LOCALAPPDATA%\inferd` and adds it to the Startup folder.
- inferd has no Intel macOS build, and the installer says so and goes on.
- Without systemd, as in a container, the installer says how to start the daemon rather than failing.

## Acceptance

- **Windows x64:** the portable `tesseract.exe` is 4.47 MB and imports `KERNEL32.dll` alone. With the model and licences, the bundle is 8.4 MB.
- **Linux x64:** built on Ubuntu 22.04, it links `libc`, `libm` and the loader alone. It reads the scan fixture on clean `debian:12-slim`, `fedora:41` and `ubuntu:24.04` images, with nothing installed.
- **The benchmark:** with the bundle's Tesseract, after the TSV fix, the means over the 33 fixtures equal the dynamic build's in every digit: CER 0.0030, recall 0.995, precision 0.998, order 1.000, blocks 0.968, headings 0.978, tables 0.959 and style 0.967.
- **Unpacked:** with `PATH` holding only `System32` and nothing set, the unpacked layout's `probe` reports `tesseract 5.5.3 eng`, and `md` reads the scan.
- **Installed:** from a mock release, `install.sh` on a clean Debian, as a fresh user, puts Tesseract in `~/.local/share/pdfspec/tesseract`. `install.ps1` puts it in `bin\tesseract`. In both, pdfspec finds it with nothing set.
- **On every platform:** CI's `build.yml` runs the same checks on all six targets.

## Consequences

- `pdfspec` reads scans as installed, on every platform it ships for, with the build its figures are measured on.
- A Tesseract bump is a line in `versions.txt`. It rebuilds and re-tests on all six targets before it can be released.
- `go install` builds pdfspec alone, and reads scans with a Tesseract on PATH. The README says so.
- Image descriptions remain the user's choice of a 6 GB model, offered at install and named where `probe` finds none.
