# Fetches the reference test PDFs listed in manifest.json.
#
# Every file is pinned to the upstream commit it was taken from, so a fetch a year
# from now retrieves the same bytes rather than whatever HEAD has become. The
# manifest records a SHA-256 for each file and this script verifies it: an upstream
# force-push or a corrupted download is then a reported mismatch rather than a
# silent change to what the tests are measured against.
#
# The committed files are already in the repo — this script exists to re-verify them
# and to fetch anything the manifest gains later.
#
#   pwsh testdata/fetch.ps1            verify what is present
#   pwsh testdata/fetch.ps1 -Download  fetch anything missing, then verify

param([switch]$Download)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$manifest = Get-Content (Join-Path $root 'manifest.json') -Raw | ConvertFrom-Json

$ok = 0; $bad = 0; $missing = 0

foreach ($src in $manifest.sources) {
    foreach ($f in $src.files) {
        $dest = Join-Path $root $f.path
        $url = "https://raw.githubusercontent.com/$($src.repo)/$($src.commit)/$($f.upstream)"

        if (-not (Test-Path $dest)) {
            if (-not $Download) {
                "MISSING  $($f.path)"
                $missing++
                continue
            }
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $dest) | Out-Null
            Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing
        }

        $got = (Get-FileHash -Path $dest -Algorithm SHA256).Hash.ToLower()
        if ($got -eq $f.sha256) {
            $ok++
        } else {
            "MISMATCH $($f.path)`n  want $($f.sha256)`n  got  $got"
            $bad++
        }
    }
}

"$ok verified, $bad mismatched, $missing missing"
if ($bad -gt 0 -or $missing -gt 0) { exit 1 }
