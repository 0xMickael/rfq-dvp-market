# Windows PowerShell 5.1. Keep this terminal open; Ctrl+C runs the finally cleanup.
[CmdletBinding()]
param(
    # Optional bounded session for automated startup/shutdown checks.
    [ValidateRange(0, 86400)][int]$RunSeconds = 0
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$oldPath = $env:Path
$oldEngineAddr = $env:ENGINE_ADDR
$oldBroadcast = $env:FOUNDRY_BROADCAST
$oldDeploymentFile = $env:DEPLOYMENT_FILE
$savedDurability=@{}
foreach($key in @('DATABASE_URL','MARKET_ID','RFQ_TEST_MODE','RFQ_TEST_FAILPOINT')){$savedDurability[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
$deploymentPath = Join-Path $root 'contracts/deployments/31337.json'
$deploymentTouched = $false
$previousDeployment = $null
$lock = [System.Threading.Mutex]::new($false, 'Local\rfq-dvp-market-demo')
$ownsLock = $false

function Assert-Processes {
    foreach ($p in $processes) {
        if ($p.HasExited) { throw "A service exited (PID $($p.Id), exit code $($p.ExitCode)). Logs: $logs" }
    }
}

function Assert-Ports {
    foreach ($port in @(8545, 8081, 8080, 5173)) {
        $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port)
        try {
            $probe.Server.ExclusiveAddressUse = $true
            $probe.Start()
        } catch { throw "Port $port is busy. Stop that service before retrying; existing processes will not be stopped." }
        finally { $probe.Stop() }
    }
}

function Invoke-Build([string]$Directory, [string]$Command, [string[]]$Arguments) {
    Push-Location (Join-Path $root $Directory)
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$Command failed (exit code $LASTEXITCODE)." }
    } finally { Pop-Location }
}

function Start-ServiceProcess([string]$Name, [string]$File, [string[]]$Arguments, [string]$Directory) {
    $options = @{
        FilePath = $File
        WorkingDirectory = $Directory
        WindowStyle = 'Hidden'
        PassThru = $true
        RedirectStandardOutput = (Join-Path $logs "$Name.out.log")
        RedirectStandardError = (Join-Path $logs "$Name.err.log")
    }
    if ($Arguments.Count -gt 0) { $options.ArgumentList = $Arguments }
    $p = Start-Process @options
    $processes.Add($p)
}

function Wait-Ready([string]$Name, [scriptblock]$Check) {
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        Assert-Processes
        try { if (& $Check) { Assert-Processes; return } } catch { }
        Start-Sleep -Milliseconds 300
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Name unavailable after 60 seconds. Logs: $logs"
}

try {
    Write-Host 'Ephemeral mode: use scripts/start.ps1 to preserve trades.'
    $env:DATABASE_URL=$null; $env:MARKET_ID=$null; $env:RFQ_TEST_MODE=$null; $env:RFQ_TEST_FAILPOINT=$null
    try { $ownsLock = $lock.WaitOne(0) }
    catch [System.Threading.AbandonedMutexException] { $ownsLock = $true }
    if (-not $ownsLock) { throw 'Another demo.ps1 session is already running.' }

    # Pick up tools installed since VS Code started, without changing the saved PATH.
    $env:Path = $oldPath + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
    $tools = @{}
    foreach ($name in @('anvil', 'forge', 'cargo', 'go', 'node', 'npm.cmd')) {
        $command = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $command) { throw "Missing tool: $name. See docs/development.md." }
        $tools[$name] = $command.Source
    }
    foreach ($file in @('contracts/lib/openzeppelin-contracts/contracts/token/ERC20/ERC20.sol', 'contracts/lib/forge-std/src/Script.sol', 'web/node_modules/vite/bin/vite.js', 'web/node_modules/typescript/bin/tsc')) {
        if (-not (Test-Path -LiteralPath (Join-Path $root $file))) {
            throw "Missing dependency: $file. Follow the setup instructions in docs/development.md."
        }
    }
    Assert-Ports
    $logs = Join-Path $root ('.cache/demo/' + [DateTime]::Now.ToString('yyyyMMdd-HHmmss') + '-' + $PID)
    New-Item -ItemType Directory -Force -Path $logs | Out-Null
    Write-Host "Building and preparing the demo. Logs: $logs"
    Invoke-Build 'market-engine' $tools.cargo @('build', '--locked', '--target-dir', '../.cache/stage1-rust')
    Invoke-Build 'backend' $tools.go @('build', '-o', '../.cache/demo-api.exe', './cmd/rfq-dvp-market-api')
    Invoke-Build 'contracts' $tools.forge @('build')
    Invoke-Build 'web' $tools.'npm.cmd' @('run', 'build')
    Assert-Ports

    Start-ServiceProcess 'anvil' $tools.anvil @('--host','127.0.0.1','--port','8545','--chain-id','31337','--hardfork','cancun','--block-time','1','--silent') $root
    Wait-Ready 'Anvil' {
        $r = Invoke-RestMethod 'http://127.0.0.1:8545' -Method Post -ContentType 'application/json' -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 2
        $r.result -eq '0x7a69'
    }
    $previousDeployment = if (Test-Path -LiteralPath $deploymentPath) { [IO.File]::ReadAllBytes($deploymentPath) } else { $null }
    $deploymentTouched = $true
    # Separate broadcast/cache files avoid Windows file locks between sessions.
    $env:FOUNDRY_BROADCAST = Join-Path $logs 'broadcast'
    $env:DEPLOYMENT_FILE = $deploymentPath.Replace('\','/')
    Invoke-Build 'contracts' $tools.forge @('script', 'script/DeployLocal.s.sol:DeployLocal', '--rpc-url', 'http://127.0.0.1:8545', '--broadcast', '--non-interactive', '--cache-path', (Join-Path $logs 'forge-cache'))
    $env:FOUNDRY_BROADCAST = $oldBroadcast
    $env:DEPLOYMENT_FILE = $oldDeploymentFile
    Assert-Processes
    $env:ENGINE_ADDR = '127.0.0.1:8081'
    Start-ServiceProcess 'rust' (Join-Path $root '.cache/stage1-rust/debug/rfq-dvp-market-engine.exe') @() $root
    Wait-Ready 'Rust' { (Invoke-RestMethod 'http://127.0.0.1:8081/healthz' -TimeoutSec 2).status -eq 'ok' }
    Start-ServiceProcess 'go' (Join-Path $root '.cache/demo-api.exe') @('-deployment','contracts/deployments/31337.json','-rpc','http://127.0.0.1:8545','-listen','127.0.0.1:8080','-engine','http://127.0.0.1:8081') $root
    Wait-Ready 'Go' { (Invoke-RestMethod 'http://127.0.0.1:8080/api/config' -TimeoutSec 2).settlement_enabled -eq $true }
    Start-ServiceProcess 'web' $tools.node @('node_modules/vite/bin/vite.js','preview','--host','127.0.0.1','--port','5173','--strictPort') (Join-Path $root 'web')
    Wait-Ready 'Frontend' { (Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:5173' -TimeoutSec 2).StatusCode -eq 200 }
    Wait-Ready 'API via frontend' { (Invoke-RestMethod 'http://127.0.0.1:5173/api/config' -TimeoutSec 2).settlement_enabled -eq $true }
    $book = Invoke-RestMethod 'http://127.0.0.1:5173/api/quotes' -Method Post -ContentType 'application/json' -Body '{"side":"BUY","unit":"tBILL","amount":"100"}' -TimeoutSec 5
    if ($book.quotes.Count -ne 3) { throw 'Quote smoke check failed.' }
    Write-Host "`nDEMO READY: http://127.0.0.1:5173"
    Write-Host 'MetaMask: Anvil 31337, trader account 0x70997970C51812dc3A010C7d01b50e0d17dc79C8'
    Write-Host 'New session: balances reset. Close tabs from previous demo sessions.'
    Write-Host 'Keep this terminal open. Ctrl+C stops all four services.'
    $started = [DateTime]::UtcNow
    while ($RunSeconds -eq 0 -or ([DateTime]::UtcNow - $started).TotalSeconds -lt $RunSeconds) {
        Assert-Processes
        Start-Sleep -Milliseconds 300
    }
} finally {
    for ($i = $processes.Count - 1; $i -ge 0; $i--) {
        $p = $processes[$i]
        try { if (-not $p.HasExited) { $p.Kill(); $p.WaitForExit() } }
        catch { Write-Warning "Stopping process $($p.Id): $_" }
        finally { $p.Dispose() }
    }
    if ($deploymentTouched) {
        if ($null -ne $previousDeployment) { [IO.File]::WriteAllBytes($deploymentPath, $previousDeployment) }
        elseif (Test-Path -LiteralPath $deploymentPath) { Remove-Item -LiteralPath $deploymentPath }
    }
    $env:ENGINE_ADDR = $oldEngineAddr
    $env:FOUNDRY_BROADCAST = $oldBroadcast
    $env:DEPLOYMENT_FILE = $oldDeploymentFile
    $env:Path = $oldPath
    foreach($key in $savedDurability.Keys){[Environment]::SetEnvironmentVariable($key,$savedDurability[$key],'Process')}
    if ($ownsLock) { $lock.ReleaseMutex() }
    $lock.Dispose()
    if ($processes.Count -gt 0) { Write-Host 'Session ended. Demo services stopped.' }
}
