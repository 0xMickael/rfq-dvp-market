# Native binaries only; no service, global PATH change or administrator password.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = Split-Path $PSScriptRoot
$destination = Join-Path $root '.cache/tools/postgresql-18.7'
if (Test-Path (Join-Path $destination 'pgsql/bin/pg_ctl.exe')) { Write-Host "PostgreSQL available: $destination"; return }
$zip = Join-Path $root '.cache/tools/postgresql-18.7.zip'
New-Item -ItemType Directory -Force (Split-Path $zip) | Out-Null
Write-Host 'Downloading PostgreSQL binaries from EDB (maximum 5 minutes)...'
$timer = [Diagnostics.Stopwatch]::StartNew()
& curl.exe --fail --location --silent --show-error --connect-timeout 30 --max-time 300 --output $zip 'https://sbp.enterprisedb.com/getfile.jsp?fileid=1260664'
if ($LASTEXITCODE -ne 0) { throw "PostgreSQL download failed (curl exit $LASTEXITCODE)." }
Write-Host ('Download completed in {0:N1}s. Checking SHA-256...' -f $timer.Elapsed.TotalSeconds)
# Archive downloaded from EDB, pinned after inspection on 2026-11-16.
if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne 'EAD0BBC27FB678FA24DCF86B738981323042ABD073C744103BF7F3AED23F9A33') { throw 'PostgreSQL archive does not match the pinned SHA-256.' }
Write-Host 'SHA-256 verified. Extracting PostgreSQL binaries...'
Expand-Archive -LiteralPath $zip -DestinationPath $destination
Write-Host ('PostgreSQL binaries prepared in {0:N1}s total.' -f $timer.Elapsed.TotalSeconds)
& (Join-Path $destination 'pgsql/bin/pg_ctl.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Invalid PostgreSQL installation.' }
