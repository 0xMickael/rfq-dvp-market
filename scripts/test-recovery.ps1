[CmdletBinding()]
param([string]$ApiBinary='', [string]$EngineBinary='', [string]$RustTargetDirectory='')
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot
if (-not $ApiBinary) { $ApiBinary=Join-Path $root '.cache/durable-api.exe' }
if (-not $EngineBinary) { $EngineBinary=Join-Path $root '.cache/verification-rust/debug/rfq-dvp-market-engine.exe' }
if (-not $RustTargetDirectory) { $RustTargetDirectory=Join-Path $root '.cache/verification-rust' }
$run=Join-Path $root ('.cache/recovery/'+[Guid]::NewGuid().ToString('N'))
$cluster=Join-Path $run 'postgres'
$oldPath=$env:Path
$oldTestDatabase=$env:RFQ_TEST_DATABASE_URL
$started=$false
try {
    $env:Path += ';'+[Environment]::GetEnvironmentVariable('Path','Machine')+';'+[Environment]::GetEnvironmentVariable('Path','User')
    foreach($port in @(28545,28546,28080,28081,55434)) {
        $probe=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,$port)
        try {$probe.Server.ExclusiveAddressUse=$true;$probe.Start()} finally {$probe.Stop()}
    }
    New-Item -ItemType Directory -Force $run | Out-Null
    & "$PSScriptRoot/postgres.ps1" -Action Init -DataDirectory $cluster -Port 55434
    & "$PSScriptRoot/postgres.ps1" -Action Start -DataDirectory $cluster -Port 55434
    $started=$true
    & "$PSScriptRoot/postgres.ps1" -Action Migrate -Port 55434
    & (Join-Path $root '.cache/tools/postgresql-18.7/pgsql/bin/createdb.exe') -h 127.0.0.1 -p 55434 -U rfq rust_recovery
    if ($LASTEXITCODE -ne 0) { throw 'Rust test database creation failed.' }
    $env:RFQ_TEST_DATABASE_URL='postgresql://rfq@127.0.0.1:55434/rust_recovery?sslmode=disable&connect_timeout=3'
    Push-Location (Join-Path $root 'market-engine')
    try { & cargo test --locked --test durability --target-dir $RustTargetDirectory -- --ignored; if($LASTEXITCODE -ne 0){throw 'Rust PostgreSQL test failed.'} } finally {Pop-Location}
    & node (Join-Path $root 'web/tests/recovery.mjs') $run $ApiBinary $EngineBinary
    if ($LASTEXITCODE -ne 0) { throw "Recovery tests failed. Logs: $run" }
    Write-Host "RECOVERY PASSED. Report: $run/summary.json"
} finally {
    if ($started) { & "$PSScriptRoot/postgres.ps1" -Action Stop -DataDirectory $cluster -Port 55434 }
    $env:Path=$oldPath
    $env:RFQ_TEST_DATABASE_URL=$oldTestDatabase
}
