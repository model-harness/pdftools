<#
.SYNOPSIS
  Builds Tesseract and its minimal dependency chain from source on Windows, x64 MSVC, no curl.

.DESCRIPTION
  Built from source rather than taken from vcpkg or a prebuilt installer, because the vcpkg port
  lists curl as a dependency rather than a feature, so `vcpkg install tesseract` cannot produce a
  build without it. An OCR engine reading a page image has no business with a network stack, and
  every library linked in is CVE surface to patch.

  Upstream CMake exposes exactly the switches needed:

    DISABLE_CURL=ON            no libcurl
    DISABLE_ARCHIVE=ON         no libarchive
    BUILD_TRAINING_TOOLS=OFF   no cairo, pango, icu, fontconfig
    DISABLE_TIFF=ON            no libtiff

  Leptonica is trimmed the same way. It is only ever handed PNG, so giflib, webp, openjpeg and
  tiff are all disabled. What remains is the required set:

    leptonica  <- Tesseract's image layer, mandatory
    libpng     <- the input format
    zlib       <- libpng's compressor

  Every archive, and the English model, is pinned by version and SHA-256 in versions.txt, beside
  this script and shared with build.sh, so both build the same engine and a bump is one reviewable
  change.

  The result is <Root>\install: bin\tesseract.exe and its DLLs, include\ and lib\ for linking
  against libtesseract, and share\tessdata\eng.traineddata.

.PARAMETER Clean
  Delete the source and build trees first. Otherwise the build is incremental.

.PARAMETER Root
  Where the sources, build trees and install go. Default: build\native at the repository root.
#>
[CmdletBinding()]
param(
    [switch]$Clean,
    [string]$Root = (Join-Path (Split-Path (Split-Path $PSScriptRoot)) 'build\native')
)

$ErrorActionPreference = 'Stop'

# versions.txt is the one place a version or hash is written: "name version sha256 url", with
# {v} in the URL standing for the version.
$Pins = @{}
foreach ($line in Get-Content (Join-Path $PSScriptRoot 'versions.txt')) {
    if ($line -match '^\s*(#|$)') { continue }
    $name, $version, $sha, $url = -split $line
    $Pins[$name] = @{ Version = $version; Sha = $sha; Url = $url.Replace('{v}', $version) }
}

$Src     = Join-Path $Root 'src'
$Build   = Join-Path $Root 'build'
$Install = Join-Path $Root 'install'

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Note($msg) { Write-Host "    $msg" -ForegroundColor DarkGray }

if ($Clean -and (Test-Path $Root)) {
    Step 'Removing previous native build'
    Remove-Item $Root -Recurse -Force
}
New-Item -ItemType Directory -Force $Src, $Build, $Install | Out-Null

# ---------------------------------------------------------------- toolchain
# A C++ source build needs the whole toolchain, cl.exe, INCLUDE and LIB, so vcvars64 is imported
# here. It is found with vswhere rather than at a fixed path, because the Build Tools and the full
# Visual Studio install in different places.
Step 'Importing the MSVC environment'
$vswhere = 'C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe'
if (-not (Test-Path $vswhere)) { throw 'vswhere not found; Visual Studio or the Build Tools are required' }

$vsPath = & $vswhere -latest -products * `
    -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (-not $vsPath) { throw 'no MSVC x64 toolset found' }
Note "visual studio: $vsPath"

$vcvars = Join-Path $vsPath 'VC\Auxiliary\Build\vcvars64.bat'
if (-not (Test-Path $vcvars)) { throw "vcvars64.bat not found at $vcvars" }

# vcvars is a batch file, so its exports are read back out of a cmd session and applied to this
# one. Only the variables that matter are copied, to avoid clobbering the session wholesale.
& cmd /c "`"$vcvars`" >nul 2>&1 && set" | ForEach-Object {
    if ($_ -match '^(PATH|INCLUDE|LIB|LIBPATH|VSCMD_ARG_TGT_ARCH|WindowsSdkDir|WindowsSDKVersion|VCToolsInstallDir)=(.*)$') {
        Set-Item -Path "env:$($Matches[1])" -Value $Matches[2]
    }
}

$cl = (Get-Command cl.exe -ErrorAction SilentlyContinue)?.Source
if (-not $cl) { throw 'cl.exe still not on PATH after importing vcvars64' }
Note "compiler: $cl"

$cmake = (Get-Command cmake.exe -ErrorAction SilentlyContinue)?.Source
if (-not $cmake) {
    $candidate = Join-Path $vsPath 'Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe'
    if (Test-Path $candidate) { $cmake = $candidate }
}
if (-not $cmake) { throw 'cmake not found. Install it, or the VS Build Tools CMake component.' }
Note "cmake: $cmake"

# Ninja is much faster than the VS generator on these trees, but only usable if CMake can invoke
# it, so its path is passed explicitly rather than left to PATH. Without it, CMake's default
# generator is the newest Visual Studio it knows, which names no version here to go stale.
$ninja = (Get-Command ninja.exe -ErrorAction SilentlyContinue)?.Source
if (-not $ninja) {
    $candidate = Join-Path $vsPath 'Common7\IDE\CommonExtensions\Microsoft\CMake\Ninja\ninja.exe'
    if (Test-Path $candidate) { $ninja = $candidate }
}
$generator = if ($ninja) { @('-G', 'Ninja', "-DCMAKE_MAKE_PROGRAM=$ninja") } else { @('-A', 'x64') }
Note "generator: $(if ($ninja) { "Ninja ($ninja)" } else { "CMake's default Visual Studio" })"

# ---------------------------------------------------------------- fetch
# Get-Pinned downloads a pin to dir and checks its SHA-256 before anything reads it.
function Get-Pinned([string]$name, [string]$dir) {
    $pin = $Pins[$name]
    $file = Join-Path $dir (Split-Path $pin.Url -Leaf)
    if (-not (Test-Path $file)) {
        Step "Downloading $name $($pin.Version)"
        Invoke-WebRequest -Uri $pin.Url -OutFile $file -UseBasicParsing
    }
    $sha = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($sha -ne $pin.Sha) {
        Remove-Item $file -Force
        throw "$name download has SHA-256 $sha, want $($pin.Sha). Check versions.txt."
    }
    return $file
}

function Get-Source([string]$name, [string]$folder) {
    $target = Join-Path $Src $folder
    $archive = Get-Pinned $name $Src
    if (Test-Path $target) { Note "$name already unpacked"; return $target }
    Step "Unpacking $name"
    # Windows' own tar, by path: from Git Bash, PATH finds GNU tar first, which reads "C:" as a
    # remote host.
    & (Join-Path $env:SystemRoot 'System32\tar.exe') -xf $archive -C $Src
    if ($LASTEXITCODE -ne 0) { throw "unpacking $name failed" }
    if (-not (Test-Path $target)) { throw "unpacked $name but $target is missing" }
    return $target
}

$zlibSrc = Get-Source 'zlib'      "zlib-$($Pins.zlib.Version)"
$pngSrc  = Get-Source 'libpng'    "libpng-$($Pins.libpng.Version)"
$leptSrc = Get-Source 'leptonica' "leptonica-$($Pins.leptonica.Version)"
$tessSrc = Get-Source 'tesseract' "tesseract-$($Pins.tesseract.Version)"

# ---------------------------------------------------------------- build helper
function Invoke-CMakeBuild([string]$name, [string]$source, [string[]]$options) {
    $dir = Join-Path $Build $name
    New-Item -ItemType Directory -Force $dir | Out-Null

    Step "Configuring $name"
    $cmakeArgs = @(
        '-S', $source
        '-B', $dir
        "-DCMAKE_INSTALL_PREFIX=$Install"
        "-DCMAKE_PREFIX_PATH=$Install"
        '-DCMAKE_BUILD_TYPE=Release'
        # The dynamic CRT, as a Rust or Go program linking libtesseract uses, since mixing the
        # static and dynamic CRT in one process is a crash waiting to happen.
        '-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreadedDLL'
    ) + $generator + $options

    & $cmake @cmakeArgs
    if ($LASTEXITCODE -ne 0) { throw "$name configure failed" }

    Step "Building $name"
    & $cmake --build $dir --config Release --parallel
    if ($LASTEXITCODE -ne 0) { throw "$name build failed" }

    & $cmake --install $dir --config Release
    if ($LASTEXITCODE -ne 0) { throw "$name install failed" }
}

# ---------------------------------------------------------------- zlib
Invoke-CMakeBuild 'zlib' $zlibSrc @(
    '-DZLIB_BUILD_EXAMPLES=OFF'
)

# ---------------------------------------------------------------- libpng
Invoke-CMakeBuild 'libpng' $pngSrc @(
    '-DPNG_SHARED=ON'
    '-DPNG_STATIC=OFF'
    '-DPNG_TESTS=OFF'
    '-DPNG_TOOLS=OFF'
    "-DZLIB_ROOT=$Install"
)

# ---------------------------------------------------------------- leptonica
# Only PNG is enabled. Leptonica can read a dozen formats, but the only image it is handed is a
# PNG of a rendered page, so every other codec is dead weight and extra CVE surface.
Invoke-CMakeBuild 'leptonica' $leptSrc @(
    '-DBUILD_PROG=OFF'
    '-DSW_BUILD=OFF'
    '-DENABLE_ZLIB=ON'
    '-DENABLE_PNG=ON'
    '-DENABLE_GIF=OFF'
    '-DENABLE_JPEG=OFF'
    '-DENABLE_TIFF=OFF'
    '-DENABLE_WEBP=OFF'
    '-DENABLE_OPENJPEG=OFF'
)

# ---------------------------------------------------------------- tesseract
# The options that matter are the disables. DISABLE_CURL is the one this script exists for.
Invoke-CMakeBuild 'tesseract' $tessSrc @(
    '-DDISABLE_CURL=ON'
    '-DDISABLE_ARCHIVE=ON'
    '-DDISABLE_TIFF=ON'
    '-DBUILD_TRAINING_TOOLS=OFF'
    '-DBUILD_TESTS=OFF'
    '-DGRAPHICS_DISABLED=ON'
    # The legacy engine is the pre-4.0 pattern matcher. LSTM is what is used, and dropping legacy
    # removes a large amount of code that would otherwise be shipped and patched.
    '-DDISABLED_LEGACY_ENGINE=ON'
    '-DOPENMP_BUILD=OFF'
    "-DLeptonica_DIR=$Install/lib/cmake/leptonica"
)

# ---------------------------------------------------------------- model
$TessData = Join-Path $Install 'share\tessdata'
New-Item -ItemType Directory -Force $TessData | Out-Null
$null = Get-Pinned 'eng' $TessData

# ---------------------------------------------------------------- verify
Step 'Verifying the result'
$dlls = Get-ChildItem (Join-Path $Install 'bin') -Filter *.dll -ErrorAction SilentlyContinue
Write-Host "`nShipped libraries:" -ForegroundColor Green
$dlls | ForEach-Object { "  {0,-34} {1,7:N2} MB" -f $_.Name, ($_.Length / 1MB) }
"  {0,-34} {1,7:N2} MB" -f 'TOTAL', (($dlls | Measure-Object -Property Length -Sum).Sum / 1MB)

# The point of the exercise: no curl and no archive linked in. libtesseract is a static library
# here, so what it links shows in the imports of the executable it is built into, not of a DLL.
# dumpbin is on PATH from vcvars.
Write-Host "`nDependency check (must not name libcurl or archive):" -ForegroundColor Green
$exe = Join-Path $Install 'bin\tesseract.exe'
if (-not (Test-Path $exe)) { throw "no $exe" }
$imports = & dumpbin /DEPENDENTS $exe 2>&1 | Where-Object { $_ -match '\.dll' }
$imports | ForEach-Object { "  $($_.Trim())" }
if ($imports -match 'curl|archive') { throw 'libcurl or libarchive is linked' }
Write-Host '  OK: no curl, no archive' -ForegroundColor Green

# Tesseract's version lists the libraries compiled in, static ones included, which imports cannot.
$version = & $exe --version 2>&1
if ($LASTEXITCODE -ne 0) { throw "$exe does not run" }
$version | ForEach-Object { "  $_" }
if ($version -match 'curl|archive') { throw 'tesseract reports libcurl or libarchive' }

Write-Host "`nInstall prefix: $Install" -ForegroundColor Green
Write-Host @"

For pdfspec:
  `$env:PDFSPEC_TESSERACT = '$exe'
  `$env:TESSDATA_PREFIX   = '$TessData'
"@ -ForegroundColor Yellow
