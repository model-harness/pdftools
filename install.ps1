# Installs pdfspec on Windows from its GitHub release: the build for this machine, checked against
# the release's checksums before it is installed, with the Tesseract it ships for scans.
#
#   irm https://raw.githubusercontent.com/model-harness/pdftools/main/install.ps1 | iex
#
# PDFSPEC_VERSION picks a release, such as v1.0.0 (default: the latest), and PDFSPEC_INSTALL_DIR
# where the binary goes (default: %LOCALAPPDATA%\pdfspec\bin). Tesseract goes beside it.
# PDFSPEC_DOWNLOAD_URL takes the release's files from a mirror of them instead of GitHub.
#
# PDFSPEC_WITH_INFERD=1 also installs inferd (https://inferd.io), the local model daemon pdfspec
# asks to describe pictures and charts. It runs at your login and, on first start, downloads its
# model, about 6 GB. Without it pdfspec reads everything else.
#
#   $env:PDFSPEC_WITH_INFERD = '1'; irm https://raw.githubusercontent.com/model-harness/pdftools/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$repo = 'model-harness/pdftools'
$version = if ($env:PDFSPEC_VERSION) { $env:PDFSPEC_VERSION } else { 'latest' }
$dir = if ($env:PDFSPEC_INSTALL_DIR) { $env:PDFSPEC_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'pdfspec\bin' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "pdfspec: no build for $($env:PROCESSOR_ARCHITECTURE)." }
}
$name = "pdfspec_windows_$arch"
$base = if ($env:PDFSPEC_DOWNLOAD_URL) { $env:PDFSPEC_DOWNLOAD_URL } elseif ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp "$name.zip"
    $sums = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Uri "$base/$name.zip" -OutFile $zip -UseBasicParsing
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing

    # sha256sum marks a name read in binary mode with "*", as it does on Windows.
    $line = Get-Content $sums | Where-Object { $_ -match " \*?$([regex]::Escape("$name.zip"))$" } | Select-Object -First 1
    $want = if ($line) { ($line -split '\s+')[0] } else { '' }
    $got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if (-not $want -or $got -ne $want) {
        throw "pdfspec: $name.zip does not match the release's checksum; nothing was installed."
    }

    Expand-Archive -Path $zip -DestinationPath $tmp
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'pdfspec.exe'
    Copy-Item -Path (Join-Path $tmp "$name\pdfspec.exe") -Destination $exe -Force
    # Where pdfspec looks for the Tesseract it ships: beside itself. A release from before
    # Tesseract was bundled has none, and pdfspec then looks on PATH.
    $tess = Join-Path $tmp "$name\tesseract"
    if (Test-Path $tess) {
        $to = Join-Path $dir 'tesseract'
        if (Test-Path $to) { Remove-Item -Recurse -Force -Path $to }
        Copy-Item -Recurse -Path $tess -Destination $to
    }
    $installed = & $exe version | Select-Object -First 1
    Write-Output "installed $installed to $exe"
    if (($env:PATH -split ';') -notcontains $dir) {
        Write-Output "add $dir to your PATH to run it as pdfspec"
    }

    if ($env:PDFSPEC_WITH_INFERD -eq '1') {
        # inferd, at the release pdfspec is tested with, checked against that release's hashes as
        # pinned here, then installed by its own installer as https://inferd.io describes. It
        # stages the daemon in %LOCALAPPDATA%\inferd and adds it to the Startup folder.
        $iv = 'v0.8.0'
        $target, $isha = switch ($arch) {
            'amd64' { 'x86_64-pc-windows-msvc', '74c1001957cb445d66af28631f9c10477c54eb244b6d1a4feb72634b58468481' }
            'arm64' { 'aarch64-pc-windows-msvc', 'ff322487a03ae4e3296fc86544fddaf844487ac1f1b3156da7ffe8e277aed0ae' }
        }
        $pkg = "inferd-$iv-$target"
        $izip = Join-Path $tmp "$pkg.zip"
        Write-Output "downloading inferd $iv"
        Invoke-WebRequest -Uri "https://github.com/3rg0n/inferd/releases/download/$iv/$pkg.zip" -OutFile $izip -UseBasicParsing
        if ((Get-FileHash -Path $izip -Algorithm SHA256).Hash.ToLowerInvariant() -ne $isha) {
            throw "inferd: $pkg.zip does not match its pinned checksum; inferd was not installed."
        }
        Expand-Archive -Path $izip -DestinationPath $tmp
        $from = Join-Path $tmp $pkg
        Push-Location $from
        try {
            & powershell -NoProfile -ExecutionPolicy Bypass -File .\packaging\install.ps1 -SourceBinary .\inferd-daemon.exe
            if ($LASTEXITCODE -ne 0) { throw "inferd's installer failed" }
        }
        finally { Pop-Location }
        $ctl = Join-Path $env:LOCALAPPDATA 'inferd\inferdctl.exe'
        Copy-Item -Path (Join-Path $from 'inferdctl.exe') -Destination $ctl -Force
        Write-Output "installed inferd $iv. On first start it downloads its model, about 6 GB; follow it with: $ctl watch"
    }
}
finally {
    Remove-Item -Recurse -Force -Path $tmp
}
