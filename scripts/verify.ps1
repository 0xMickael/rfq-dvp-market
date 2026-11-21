# Reproducible Windows checks. No global installation or source rewriting.
[CmdletBinding()]
param([switch]$SkipIntegration,[string]$RustTargetDirectory='')
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$run = Join-Path $root ('.cache/verification/' + [Guid]::NewGuid().ToString('N'))
$target = Join-Path $root '.cache/verification-rust'
if ($RustTargetDirectory) { $target = [IO.Path]::GetFullPath($RustTargetDirectory) }
$oldPath = $env:Path
$oldGoCache = $env:GOCACHE
$steps = [System.Collections.Generic.List[object]]::new()
$passed = $false
$transcribing = $false
$mutex = [Threading.Mutex]::new($false,'Local\rfq-dvp-market-verify')
$ownsMutex = $false
function Run-Check([string]$Name, [string]$Directory, [scriptblock]$Action) {
    Write-Host "Verification : $Name"
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $ok = $false
    Push-Location (Join-Path $root $Directory)
    try { & $Action; $ok = $true }
    finally {
        Pop-Location
        $steps.Add([pscustomobject]@{name=$Name; passed=$ok; seconds=$watch.Elapsed.TotalSeconds})
    }
}
function Invoke-Checked([string]$Command, [string[]]$Arguments) {
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command echoue (code $LASTEXITCODE)." }
}
try {
    try { $ownsMutex = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $ownsMutex = $true }
    if (-not $ownsMutex) { throw 'Une verification est deja en cours.' }
    $env:Path += ';' + [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
    foreach ($command in @('git','go','gofmt','cargo','rustc','forge','anvil','node','npm.cmd')) {
        if (-not (Get-Command $command -CommandType Application -ErrorAction SilentlyContinue)) {
            throw "Outil absent : $command. Voir docs/development.md."
        }
    }
    New-Item -ItemType Directory -Path $run -Force | Out-Null
    Start-Transcript -LiteralPath (Join-Path $run 'verify.log') | Out-Null
    $transcribing = $true
    $env:GOCACHE = Join-Path $root '.cache/go-build'
    $versions = Get-Content (Join-Path $PSScriptRoot 'toolchains.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    Run-Check 'Versions et prerequis' '.' {
        $actual = @{
            go = ((& go version) -split ' ')[2].Substring(2)
            rust = ((& rustc "+$($versions.rust)" --version) -split ' ')[1]
            node = (& node --version).TrimStart('v')
            npm = (& npm.cmd --version)
            foundry = ((& forge --version)[0] -split ' ')[2]
        }
        foreach ($name in $actual.Keys) {
            Write-Host "$name : $($actual[$name])"
            if ($actual[$name] -ne $versions.$name) { throw "$name : version $($versions.$name) requise, trouve $($actual[$name])." }
        }
        if (((& anvil --version)[0] -split ' ')[2] -ne $versions.foundry) { throw 'Version Anvil differente de Forge.' }
        foreach ($file in @('contracts/lib/forge-std/src/Script.sol','contracts/lib/openzeppelin-contracts/contracts/token/ERC20/ERC20.sol','web/node_modules/vite/bin/vite.js')) {
            if (-not (Test-Path -LiteralPath (Join-Path $root $file))) { throw "Dependance absente : $file. Voir docs/development.md." }
        }
    }
    Run-Check 'Rust : format, tests, Clippy, build' 'market-engine' {
        Invoke-Checked cargo @('fmt','--check')
        Invoke-Checked cargo @('test','--locked','--all-targets','--target-dir',$target)
        Invoke-Checked cargo @('clippy','--locked','--all-targets','--target-dir',$target,'--','-D','warnings')
        Invoke-Checked cargo @('build','--locked','--target-dir',$target)
        Copy-Item -LiteralPath (Join-Path $target 'debug/rfq-dvp-market-engine.exe') -Destination (Join-Path $run 'engine.exe')
    }
    Run-Check 'Go : format, tests, vet, build' 'backend' {
        $unformatted = & gofmt -l cmd internal
        if ($LASTEXITCODE -ne 0 -or $unformatted) { throw "Format Go incorrect : $unformatted" }
        Invoke-Checked go @('test','-count=1','./...')
        Invoke-Checked go @('vet','./...')
        Invoke-Checked go @('build','-o',(Join-Path $run 'api.exe'),'./cmd/rfq-dvp-market-api')
    }
    Run-Check 'Solidity : format, tests, build et lint' 'contracts' {
        Invoke-Checked forge @('fmt','--check')
        Invoke-Checked forge @('test','-vv')
        Invoke-Checked forge @('build')
    }
    Run-Check 'Frontend : tests, TypeScript strict, build' 'web' {
        Invoke-Checked 'npm.cmd' @('test')
        Invoke-Checked 'npm.cmd' @('run','build','--','--outDir',(Join-Path $run 'web'))
    }
    if (-not $SkipIntegration) {
        foreach ($scenario in @('Signatures','Settlement')) {
            Run-Check "Integration $scenario : vrais processus via Vite" '.' {
                & (Join-Path $PSScriptRoot 'test-app-settlement.ps1') -Scenario $scenario -ApiBinary (Join-Path $run 'api.exe') -EngineBinary (Join-Path $run 'engine.exe') -WebDist (Join-Path $run 'web')
            }
        }
        Run-Check 'PostgreSQL recovery: real Go/Rust crashes on Anvil' '.' {
            & (Join-Path $PSScriptRoot 'test-recovery.ps1') -ApiBinary (Join-Path $run 'api.exe') -EngineBinary (Join-Path $run 'engine.exe') -RustTargetDirectory $target
        }
    }
    Run-Check 'Diff sans erreurs de whitespace' '.' { Invoke-Checked git @('diff','--check') }
    $passed = $true
    Write-Host "VERIFICATIONS REUSSIES. Integration executee : $(-not $SkipIntegration). Rapport : $run"
} finally {
    if ($transcribing) {
        $summary = [ordered]@{passed=$passed; integration_requested=(-not $SkipIntegration); steps=@($steps.ToArray())}
        [IO.File]::WriteAllText((Join-Path $run 'summary.json'),($summary | ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
        Stop-Transcript | Out-Null
    }
    $env:Path = $oldPath
    $env:GOCACHE = $oldGoCache
    if ($ownsMutex) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}
