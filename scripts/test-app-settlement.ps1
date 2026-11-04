$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$processes = @()
$deploymentPath = Join-Path $root 'contracts/deployments/31337.json'
$previousDeployment = if (Test-Path $deploymentPath) { [System.IO.File]::ReadAllBytes($deploymentPath) } else { $null }
$previousEngineAddr = $env:ENGINE_ADDR
try {
    foreach ($port in @(18545,18080,18081)) {
        if (Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue) { throw "Port $port occupé : aucun processus existant ne sera arrêté." }
    }
    $processes += Start-Process -FilePath (Get-Command anvil).Source -ArgumentList @('--host','127.0.0.1','--port','18545','--chain-id','31337','--hardfork','cancun','--silent') -WindowStyle Hidden -PassThru
    $ready = $false
    for ($i=0; $i -lt 40; $i++) {
        try { $r = Invoke-RestMethod -Uri 'http://127.0.0.1:18545' -Method Post -ContentType 'application/json' -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 1; if ($r.result -eq '0x7a69') { $ready=$true; break } } catch {}
        Start-Sleep -Milliseconds 250
    }
    if (-not $ready) { throw 'Anvil indisponible' }
    Push-Location (Join-Path $root 'contracts')
    try {
        forge script script/DeployLocal.s.sol:DeployLocal --rpc-url http://127.0.0.1:18545 --broadcast --non-interactive
        if ($LASTEXITCODE -ne 0) { throw 'Déploiement échoué' }
    } finally { Pop-Location }
    Copy-Item -LiteralPath $deploymentPath -Destination (Join-Path $root '.cache/settlement-deployment.json')
    $env:ENGINE_ADDR = '127.0.0.1:18081'
    $processes += Start-Process -FilePath (Join-Path $root '.cache/stage1-rust/debug/rfq-dvp-market-engine.exe') -WindowStyle Hidden -PassThru -RedirectStandardError (Join-Path $root '.cache/settlement-rust.log')
    $processes += Start-Process -FilePath (Join-Path $root '.cache/settlement-api.exe') -ArgumentList @('-deployment','.cache/settlement-deployment.json','-rpc','http://127.0.0.1:18545','-listen','127.0.0.1:18080','-engine','http://127.0.0.1:18081') -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $root '.cache/settlement-go.log') -RedirectStandardError (Join-Path $root '.cache/settlement-go.err')
    foreach ($url in @('http://127.0.0.1:18081/healthz','http://127.0.0.1:18080/healthz')) {
        $ready = $false
        for ($i=0; $i -lt 40; $i++) {
            try { $null = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 1; $ready=$true; break } catch {}
            Start-Sleep -Milliseconds 250
        }
        if (-not $ready) { throw "Service indisponible : $url" }
    }
    & node (Join-Path $root 'web/tests/live-settlement.mjs')
    if ($LASTEXITCODE -ne 0) { throw 'Échec du settlement applicatif' }
} finally {
    foreach ($process in $processes) { if (-not $process.HasExited) { Stop-Process -Id $process.Id }; $process.Dispose() }
    $env:ENGINE_ADDR = $previousEngineAddr
    if ($null -ne $previousDeployment) { [System.IO.File]::WriteAllBytes($deploymentPath,$previousDeployment) }
    elseif (Test-Path $deploymentPath) { Remove-Item -LiteralPath $deploymentPath }
}
