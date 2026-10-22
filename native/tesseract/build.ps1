<#
.SYNOPSIS
  Builds Tesseract and its minimal dependency chain from source on Windows, MSVC, no curl.

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

  Every archive, the English model and its licence are pinned by version and SHA-256 in
  versions.txt, beside this script and shared with build.sh, so both build the same engine and a
  bump is one reviewable change.

  It builds for the machine it runs on, x64 or arm64.

  By default the result is <Root>\install: bin\tesseract.exe and its DLLs, include\ and lib\ for
  linking against libtesseract with the dynamic CRT, and share\tessdata\eng.traineddata. That is
  the build a program links libtesseract from.

  With -Portable it is the Tesseract pdfspec ships instead, <Root>\portable\tesseract: one
  tesseract.exe with the C runtime, zlib and libpng linked in, so it runs on a machine with
  nothing installed, beside tessdata\eng.traineddata and licenses\.

.PARAMETER Clean
  Delete the source and build trees first. Otherwise the build is incremental.

.PARAMETER Portable
  Build the self-contained tesseract.exe pdfspec ships, rather than the libraries to link.

.PARAMETER Root
  Where the sources, build trees and results go. Default: build\native, or build\portable with
  -Portable, at the repository root.
#>
[CmdletBinding()]
param(
    [switch]$Clean,
    [switch]$Portable,
    [string]$Root
)

$ErrorActionPreference = 'Stop'

if (-not $Root) {
    $Root = Join-Path (Split-Path (Split-Path $PSScriptRoot)) $(if ($Portable) { 'build\portable' } else { 'build\native' })
}

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
# A C++ source build needs the whole toolchain, cl.exe, INCLUDE and LIB, so vcvars is imported
# here, for the machine's own architecture. It is found with vswhere rather than at a fixed path,
# because the Build Tools and the full Visual Studio install in different places.
$arm64 = $env:PROCESSOR_ARCHITECTURE -eq 'ARM64'
$component, $vcvarsName, $platform = if ($arm64) {
    'Microsoft.VisualStudio.Component.VC.Tools.ARM64', 'vcvarsarm64.bat', 'ARM64'
} else {
    'Microsoft.VisualStudio.Component.VC.Tools.x86.x64', 'vcvars64.bat', 'x64'
}

Step "Importing the MSVC environment for $platform"
$vswhere = 'C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe'
if (-not (Test-Path $vswhere)) { throw 'vswhere not found; Visual Studio or the Build Tools are required' }

$vsPath = & $vswhere -latest -products * -requires $component -property installationPath
if (-not $vsPath) { throw "no MSVC $platform toolset found" }
Note "visual studio: $vsPath"

$vcvars = Join-Path $vsPath "VC\Auxiliary\Build\$vcvarsName"
if (-not (Test-Path $vcvars)) { throw "$vcvarsName not found at $vcvars" }

# vcvars is a batch file, so its exports are read back out of a cmd session and applied to this
# one. Only the variables that matter are copied, to avoid clobbering the session wholesale.
& cmd /c "`"$vcvars`" >nul 2>&1 && set" | ForEach-Object {
    if ($_ -match '^(PATH|INCLUDE|LIB|LIBPATH|VSCMD_ARG_TGT_ARCH|WindowsSdkDir|WindowsSDKVersion|VCToolsInstallDir)=(.*)$') {
        Set-Item -Path "env:$($Matches[1])" -Value $Matches[2]
    }
}

$cl = (Get-Command cl.exe -ErrorAction SilentlyContinue)?.Source
if (-not $cl) { throw "cl.exe still not on PATH after importing $vcvarsName" }
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
$generator = if ($ninja) { @('-G', 'Ninja', "-DCMAKE_MAKE_PROGRAM=$ninja") } else { @('-A', $platform) }
Note "generator: $(if ($ninja) { "Ninja ($ninja)" } else { "CMake's default Visual Studio" })"

# The dynamic CRT for the libraries a program links, since mixing the static and dynamic CRT in
# one process is a crash waiting to happen. The static CRT for the portable executable, so it
# needs no Visual C++ redistributable: every library in it is static too, and none hands a FILE*
# or a heap pointer across a DLL boundary to a second CRT.
$crt = if ($Portable) { 'MultiThreaded' } else { 'MultiThreadedDLL' }
Note "runtime: $crt"

# A static-only zlib installs as zs.lib, a name CMake's FindZLIB does not look for, so the builds
# after it are told where it is. Unset until zlib is built, and harmless to zlib itself.
$hints = if ($Portable) { @("-DZLIB_LIBRARY=$Install/lib/zs.lib", "-DZLIB_INCLUDE_DIR=$Install/include") } else { @() }

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
        "-DCMAKE_MSVC_RUNTIME_LIBRARY=$crt"
        # CMAKE_MSVC_RUNTIME_LIBRARY is read only under policy CMP0091, which a project declaring
        # an old minimum CMake leaves off: leptonica built with the DLL CRT without it.
        '-DCMAKE_POLICY_DEFAULT_CMP0091=NEW'
        '-DBUILD_SHARED_LIBS=OFF'
    ) + $generator + $hints + $options

    & $cmake @cmakeArgs
    if ($LASTEXITCODE -ne 0) { throw "$name configure failed" }

    Step "Building $name"
    & $cmake --build $dir --config Release --parallel
    if ($LASTEXITCODE -ne 0) { throw "$name build failed" }

    & $cmake --install $dir --config Release
    if ($LASTEXITCODE -ne 0) { throw "$name install failed" }
}

# ---------------------------------------------------------------- zlib
# Shared for the linking build, whose libpng DLL loads it; static alone for the portable one, so
# leptonica cannot find an import library to link against instead.
Invoke-CMakeBuild 'zlib' $zlibSrc @(
    '-DZLIB_BUILD_EXAMPLES=OFF'
    '-DZLIB_BUILD_TESTING=OFF'
    "-DZLIB_BUILD_SHARED=$(if ($Portable) { 'OFF' } else { 'ON' })"
    '-DZLIB_BUILD_STATIC=ON'
)

# ---------------------------------------------------------------- libpng
Invoke-CMakeBuild 'libpng' $pngSrc @(
    "-DPNG_SHARED=$(if ($Portable) { 'OFF' } else { 'ON' })"
    "-DPNG_STATIC=$(if ($Portable) { 'ON' } else { 'OFF' })"
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
$exe = Join-Path $Install 'bin\tesseract.exe'
if (-not (Test-Path $exe)) { throw "no $exe" }

# The point of the exercise: no curl and no archive linked in. libtesseract is a static library
# here, so what it links shows in the imports of the executable it is built into, not of a DLL.
# dumpbin is on PATH from vcvars.
Write-Host "`nImports (must not name libcurl or archive$(if ($Portable) { ', or any DLL Windows does not ship' })):" -ForegroundColor Green
$imports = & dumpbin /DEPENDENTS $exe 2>&1 | Where-Object { $_ -match '^\s+\S+\.dll\s*$' } | ForEach-Object { $_.Trim() }
$imports | ForEach-Object { "  $_" }
if ($imports -match 'curl|archive') { throw 'libcurl or libarchive is linked' }
if ($Portable) {
    # What a fresh Windows has: the system DLLs and the Universal CRT's api sets, never the Visual
    # C++ redistributable or a DLL of ours.
    $foreign = $imports | Where-Object { $_ -notmatch '^(KERNEL32|USER32|ADVAPI32|SHELL32|WS2_32|ole32|OLEAUT32|bcrypt|ntdll)\.dll$' -and $_ -notmatch '^api-ms-win-' }
    if ($foreign) { throw "the portable tesseract.exe imports $($foreign -join ', '), which Windows does not ship" }
}
Write-Host '  OK' -ForegroundColor Green

# Tesseract's version lists the libraries compiled in, static ones included, which imports cannot.
$version = & $exe --version 2>&1
if ($LASTEXITCODE -ne 0) { throw "$exe does not run" }
$version | ForEach-Object { "  $_" }
if ($version -match 'curl|archive') { throw 'tesseract reports libcurl or libarchive' }

if (-not $Portable) {
    Write-Host "`nInstall prefix: $Install" -ForegroundColor Green
    Write-Host @"

For pdfspec:
  `$env:PDFSPEC_TESSERACT = '$exe'
  `$env:TESSDATA_PREFIX   = '$TessData'
"@ -ForegroundColor Yellow
    return
}

# ---------------------------------------------------------------- portable layout
# What pdfspec ships beside itself: the executable, the model, and the licences of everything in
# them, which their terms require a copy to carry.
$Out = Join-Path $Root 'portable\tesseract'
Step "Laying out $Out"
if (Test-Path $Out) { Remove-Item $Out -Recurse -Force }
$licenses = Join-Path $Out 'licenses'
New-Item -ItemType Directory -Force (Join-Path $Out 'tessdata'), $licenses | Out-Null
Copy-Item $exe (Join-Path $Out 'tesseract.exe')
Copy-Item (Join-Path $TessData 'eng.traineddata') (Join-Path $Out 'tessdata\eng.traineddata')
Copy-Item (Join-Path $tessSrc 'LICENSE') (Join-Path $licenses 'tesseract.txt')
Copy-Item (Join-Path $leptSrc 'leptonica-license.txt') (Join-Path $licenses 'leptonica.txt')
Copy-Item (Join-Path $pngSrc 'LICENSE') (Join-Path $licenses 'libpng.txt')
Copy-Item (Join-Path $zlibSrc 'LICENSE') (Join-Path $licenses 'zlib.txt')
Copy-Item (Get-Pinned 'eng-license' $Src) (Join-Path $licenses 'tessdata_fast.txt')

# It must read from where it stands, with its own model and nothing else.
$smoke = & (Join-Path $Out 'tesseract.exe') --tessdata-dir (Join-Path $Out 'tessdata') --list-langs 2>&1
if ($LASTEXITCODE -ne 0 -or ($smoke -notcontains 'eng')) { throw "the portable tesseract does not list eng: $smoke" }
Write-Host "`nPortable Tesseract: $Out" -ForegroundColor Green
Get-ChildItem $Out -Recurse -File | ForEach-Object { "  {0,-40} {1,7:N2} MB" -f $_.FullName.Substring($Out.Length + 1), ($_.Length / 1MB) }
