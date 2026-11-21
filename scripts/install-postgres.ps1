# Native binaries only; no service, global PATH change or administrator password.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$destination = Join-Path $root '.cache/tools/postgresql-18.7'
if (Test-Path (Join-Path $destination 'pgsql/bin/pg_ctl.exe')) { Write-Host "PostgreSQL available: $destination"; return }
$zip = Join-Path $root '.cache/tools/postgresql-18.7.zip'
New-Item -ItemType Directory -Force (Split-Path $zip) | Out-Null
Invoke-WebRequest -UseBasicParsing 'https://sbp.enterprisedb.com/getfile.jsp?fileid=1260664' -OutFile $zip
# Archive downloaded from EDB, pinned after inspection on 2026-11-16.
if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne 'EAD0BBC27FB678FA24DCF86B738981323042ABD073C744103BF7F3AED23F9A33') { throw 'PostgreSQL archive does not match the pinned SHA-256.' }
Expand-Archive -LiteralPath $zip -DestinationPath $destination
& (Join-Path $destination 'pgsql/bin/pg_ctl.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Invalid PostgreSQL installation.' }
