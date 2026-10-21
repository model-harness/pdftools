# Installs pdfspec on Windows from its GitHub release: the build for this machine, checked against
# the release's checksums before it is installed.
#
#   irm https://raw.githubusercontent.com/model-harness/pdftools/main/install.ps1 | iex
#
# PDFSPEC_VERSION picks a release, such as v1.0.0 (default: the latest), and PDFSPEC_INSTALL_DIR
# where the binary goes (default: %LOCALAPPDATA%\pdfspec\bin).
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
$base = if ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp "$name.zip"
    $sums = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Uri "$base/$name.zip" -OutFile $zip -UseBasicParsing
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing

    $line = Get-Content $sums | Where-Object { $_ -match " $([regex]::Escape("$name.zip"))$" } | Select-Object -First 1
    $want = if ($line) { ($line -split '\s+')[0] } else { '' }
    $got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if (-not $want -or $got -ne $want) {
        throw "pdfspec: $name.zip does not match the release's checksum; nothing was installed."
    }

    Expand-Archive -Path $zip -DestinationPath $tmp
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'pdfspec.exe'
    Copy-Item -Path (Join-Path $tmp "$name\pdfspec.exe") -Destination $exe -Force
    $installed = & $exe version | Select-Object -First 1
    Write-Output "installed $installed to $exe"
    if (($env:PATH -split ';') -notcontains $dir) {
        Write-Output "add $dir to your PATH to run it as pdfspec"
    }
}
finally {
    Remove-Item -Recurse -Force -Path $tmp
}
