# Windows PowerShell 5.1. Build first with scripts/verify.ps1.
[CmdletBinding()]
param(
    [ValidateSet('Settlement', 'Signatures')][string]$Scenario = 'Settlement',
    [string]$ApiBinary = '',
    [string]$EngineBinary = '',
    [string]$WebDist = ''
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
if (-not $ApiBinary) { $ApiBinary = Join-Path $root '.cache/settlement-api.exe' }
if (-not $EngineBinary) { $EngineBinary = Join-Path $root '.cache/stage1-rust/debug/rfq-dvp-market-engine.exe' }
if (-not $WebDist) { $WebDist = Join-Path $root 'web/dist' }
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$saved = @{}
foreach ($name in @('Path','ENGINE_ADDR','FOUNDRY_BROADCAST','FOUNDRY_CACHE_PATH','DEPLOYMENT_FILE','API_PROXY_TARGET','TEST_API_URL')) {
    $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$logs = Join-Path $root ('.cache/integration/' + $Scenario.ToLowerInvariant() + '-' + [Guid]::NewGuid().ToString('N'))

function Assert-Alive {
    foreach ($p in $processes) {
        if ($p.HasExited) { throw "Service arrete (PID $($p.Id), code $($p.ExitCode)). Logs : $logs" }
    }
}
function Start-TestProcess([string]$Name, [string]$File, [string[]]$Arguments, [string]$Directory) {
    # Windows joins ArgumentList into one command line. Arguments never contain secrets.
    $quoted = @($Arguments | ForEach-Object { if ($_ -match '[\s"]') { '"' + $_.Replace('"','\"') + '"' } else { $_ } })
    $options = @{
        FilePath = $File; WorkingDirectory = $Directory; WindowStyle = 'Hidden'; PassThru = $true
        RedirectStandardOutput = (Join-Path $logs "$Name.out.log")
        RedirectStandardError = (Join-Path $logs "$Name.err.log")
    }
    if ($quoted.Count) { $options.ArgumentList = $quoted }
    $p = Start-Process @options
    $null = $p.Handle # Retain the handle so ExitCode remains available after exit on Windows PowerShell.
    $processes.Add($p)
    return $p
}
function Wait-TestReady([string]$Name, [scriptblock]$Check) {
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        Assert-Alive
        try { if (& $Check) { Assert-Alive; return } } catch { }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Name indisponible. Logs : $logs"
}
function Invoke-TestClient([string]$File) {
    $client = Start-TestProcess 'client' (Get-Command node).Source @($File) $root
    # A regression must not leave the integration waiting indefinitely.
    if (-not $client.WaitForExit(120000)) { throw "Client de test bloque. Logs : $logs" }
    $processes.Remove($client) | Out-Null
    $code = $client.ExitCode
    $client.Dispose()
    Get-Content -LiteralPath (Join-Path $logs 'client.out.log') -Encoding UTF8
    if ($code -ne 0) {
        Get-Content -LiteralPath (Join-Path $logs 'client.err.log') -Encoding UTF8
        throw "Integration $Scenario echouee (code $code). Logs : $logs"
    }
}
try {
    $env:Path += ';' + [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
    foreach ($file in @($ApiBinary,$EngineBinary,(Join-Path $WebDist 'index.html'),(Join-Path $root 'web/.test-dist/terms.js'))) {
        if (-not (Test-Path -LiteralPath $file)) { throw "Build absent : $file. Lance scripts/verify.ps1." }
    }
    foreach ($port in @(18545,18080,18081,15173)) {
        $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,$port)
        try { $probe.Server.ExclusiveAddressUse = $true; $probe.Start() }
        catch { throw "Port $port occupe : aucun service existant ne sera arrete." }
        finally { $probe.Stop() }
    }
    New-Item -ItemType Directory -Path $logs -Force | Out-Null
    Write-Host "Integration $Scenario. Logs : $logs"
    $env:FOUNDRY_BROADCAST = Join-Path $logs 'broadcast'
    $env:FOUNDRY_CACHE_PATH = Join-Path $logs 'forge-cache'
    $env:DEPLOYMENT_FILE = (Join-Path $logs 'deployment.json').Replace('\','/')
    $deployment = Join-Path $root 'backend/testdata/signing-deployment.json'
    if ($Scenario -eq 'Settlement') {
        $null = Start-TestProcess 'anvil' (Get-Command anvil).Source @('--host','127.0.0.1','--port','18545','--chain-id','31337','--hardfork','cancun','--silent') $root
        Wait-TestReady 'Anvil' {
            (Invoke-RestMethod 'http://127.0.0.1:18545' -Method Post -ContentType 'application/json' -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 2).result -eq '0x7a69'
        }
        Push-Location (Join-Path $root 'contracts')
        try {
            & forge script script/DeployLocal.s.sol:DeployLocal --rpc-url http://127.0.0.1:18545 --broadcast --non-interactive
            if ($LASTEXITCODE -ne 0) { throw 'Deploiement de test echoue.' }
        } finally { Pop-Location }
        $deployment = $env:DEPLOYMENT_FILE
    }
    $env:ENGINE_ADDR = '127.0.0.1:18081'
    $null = Start-TestProcess 'rust' $EngineBinary @() $root
    Wait-TestReady 'Rust' { (Invoke-RestMethod 'http://127.0.0.1:18081/healthz' -TimeoutSec 2).status -eq 'ok' }
    $arguments = @('-deployment',$deployment,'-listen','127.0.0.1:18080','-engine','http://127.0.0.1:18081')
    if ($Scenario -eq 'Settlement') { $arguments += @('-rpc','http://127.0.0.1:18545') }
    $null = Start-TestProcess 'go' $ApiBinary $arguments $root
    Wait-TestReady 'Go' { (Invoke-RestMethod 'http://127.0.0.1:18080/api/config' -TimeoutSec 2).signing_enabled }
    $env:API_PROXY_TARGET = 'http://127.0.0.1:18080'
    $null = Start-TestProcess 'web' (Get-Command node).Source @('node_modules/vite/bin/vite.js','preview','--host','127.0.0.1','--port','15173','--strictPort','--outDir',$WebDist) (Join-Path $root 'web')
    Wait-TestReady 'Frontend' { (Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:15173' -TimeoutSec 2).StatusCode -eq 200 }
    Wait-TestReady 'Proxy Vite' { (Invoke-RestMethod 'http://127.0.0.1:15173/api/config' -TimeoutSec 2).signing_enabled }
    $env:TEST_API_URL = 'http://127.0.0.1:15173'
    $test = if ($Scenario -eq 'Settlement') { 'live-settlement.mjs' } else { 'live-signatures.mjs' }
    Invoke-TestClient (Join-Path $root "web/tests/$test")
    Assert-Alive
} finally {
    for ($i = $processes.Count - 1; $i -ge 0; $i--) {
        $p = $processes[$i]
        try { if (-not $p.HasExited) { $p.Kill(); $p.WaitForExit() } }
        finally { $p.Dispose() }
    }
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
}