# Durable application startup: attach to an existing chain and PostgreSQL database.
[CmdletBinding()]
param([ValidateRange(0,86400)][int]$RunSeconds = 0)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$deployment = Join-Path $root '.cache/local-market/deployment.json'
$run = Join-Path $root ('.cache/start/' + [Guid]::NewGuid().ToString('N'))
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()
$saved = @{}
foreach ($key in @('Path','DATABASE_URL','MARKET_ID','ENGINE_ADDR','API_PROXY_TARGET','RFQ_TEST_MODE','RFQ_TEST_FAILPOINT')) {
    $saved[$key] = [Environment]::GetEnvironmentVariable($key,'Process')
}
function Checked([string]$Directory, [string]$Command, [string[]]$Arguments) {
    Push-Location (Join-Path $root $Directory)
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$Command failed" }
    } finally { Pop-Location }
}
function Start-App([string]$Name, [string]$File, [string[]]$Arguments, [string]$Directory) {
    $quoted = @($Arguments | ForEach-Object {
        if ($_ -match '\s') { '"' + $_ + '"' } else { $_ }
    })
    $options = @{
        FilePath = $File
        WorkingDirectory = $Directory
        WindowStyle = 'Hidden'
        PassThru = $true
        RedirectStandardOutput = "$run/$Name.out.log"
        RedirectStandardError = "$run/$Name.err.log"
    }
    if ($quoted.Count) { $options.ArgumentList = $quoted }
    $process = Start-Process @options
    $null = $process.Handle
    $processes.Add($process)
}
function Wait-URL([string]$URL) {
    for ($i = 0; $i -lt 150; $i++) {
        foreach ($process in $processes) {
            if ($process.HasExited) { throw "Service exited. Logs: $run" }
        }
        try {
            if ((Invoke-WebRequest -UseBasicParsing $URL -TimeoutSec 2).StatusCode -eq 200) { return }
        } catch { }
        Start-Sleep -Milliseconds 200
    }
    throw "Service unavailable: $URL. Logs: $run"
}
try {
    $env:Path+=';'+[Environment]::GetEnvironmentVariable('Path','Machine')+';'+[Environment]::GetEnvironmentVariable('Path','User')
    if(-not(Test-Path $deployment)){throw 'Initialize the chain with scripts/local-chain.ps1 -Initialize.'}
    foreach ($port in @(8080,8081,5173)) {
        $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,$port)
        try {
            $probe.Server.ExclusiveAddressUse = $true
            $probe.Start()
        } finally { $probe.Stop() }
    }
    New-Item -ItemType Directory -Force $run | Out-Null
    if(-not $env:DATABASE_URL){$env:DATABASE_URL='postgresql://rfq@127.0.0.1:55432/postgres?sslmode=disable&connect_timeout=3'}
    $env:RFQ_TEST_MODE = $null
    $env:RFQ_TEST_FAILPOINT = $null
    Checked 'backend' 'go' @('build','-o',"$run/api.exe",'./cmd/rfq-dvp-market-api')
    Checked 'market-engine' 'cargo' @('build','--locked','--target-dir','../.cache/verification-rust')
    Checked 'web' 'npm.cmd' @('run','build')
    $env:MARKET_ID= & "$run/api.exe" -deployment $deployment -rpc http://127.0.0.1:8545 -print-market-id
    if($LASTEXITCODE -ne 0){throw 'Chain unavailable.'}
    $env:ENGINE_ADDR = '127.0.0.1:8081'
    $env:API_PROXY_TARGET = 'http://127.0.0.1:8080'
    Start-App 'engine' (Join-Path $root '.cache/verification-rust/debug/rfq-dvp-market-engine.exe') @() $root
    Wait-URL 'http://127.0.0.1:8081/market'
    Start-App 'api' "$run/api.exe" @('-deployment',$deployment,'-rpc','http://127.0.0.1:8545') $root
    Wait-URL 'http://127.0.0.1:8080/api/config'
    Start-App 'web' (Get-Command node).Source @('node_modules/vite/bin/vite.js','preview','--host','127.0.0.1','--port','5173','--strictPort') (Join-Path $root 'web')
    Wait-URL 'http://127.0.0.1:5173'
    Write-Host "APPLICATION: http://127.0.0.1:5173. Readiness: http://127.0.0.1:8080/api/readiness. Logs: $run"
    Write-Host 'Ctrl+C stops Go, Rust and the frontend. PostgreSQL and the chain remain available.'
    $began = [DateTime]::UtcNow
    while ($RunSeconds -eq 0 -or ([DateTime]::UtcNow - $began).TotalSeconds -lt $RunSeconds) {
        foreach ($process in $processes) {
            if ($process.HasExited) { throw 'Service exited.' }
        }
        Start-Sleep -Milliseconds 300
    }
} finally {
    for ($i = $processes.Count - 1; $i -ge 0; $i--) {
        $process = $processes[$i]
        try {
            if (-not $process.HasExited) {
                $process.Kill()
                $process.WaitForExit()
            }
        } finally { $process.Dispose() }
    }
    foreach ($key in $saved.Keys) {
        [Environment]::SetEnvironmentVariable($key,$saved[$key],'Process')
    }
}
