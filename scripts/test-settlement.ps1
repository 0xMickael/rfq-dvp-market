$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$anvilProcess = $null
$rpc = 'http://127.0.0.1:18545'
$env:FOUNDRY_DISABLE_NIGHTLY_WARNING = '1'
Push-Location (Join-Path $root 'contracts')
try {
    if (Get-NetTCPConnection -State Listen -LocalPort 18545 -ErrorAction SilentlyContinue) {
        throw 'Port 18545 occupé : le test ne touche pas aux processus existants.'
    }
    $anvilProcess = Start-Process -FilePath (Get-Command anvil).Source -ArgumentList @('--host', '127.0.0.1', '--port', '18545', '--chain-id', '31337', '--hardfork', 'cancun', '--silent') -WindowStyle Hidden -PassThru
    $ready = $false
    for ($i = 0; $i -lt 40; $i++) {
        try {
            $probe = Invoke-RestMethod -Uri $rpc -Method Post -ContentType 'application/json' -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 1
            if ($probe.result -eq '0x7a69') { $ready = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    if (-not $ready) { throw 'Anvil ne démarre pas' }
    forge script script/DeployLocal.s.sol:DeployLocal --rpc-url $rpc --broadcast --non-interactive
    if ($LASTEXITCODE -ne 0) { throw 'Déploiement échoué' }
    forge script script/SettleLocal.s.sol:SettleLocal --rpc-url $rpc --broadcast --non-interactive
    if ($LASTEXITCODE -ne 0) { throw 'Settlement échoué' }
    $deployment = Get-Content -Raw deployments/31337.json | ConvertFrom-Json
    function Assert-Value($contract, $signature, $arguments, $expected) {
        $value = & cast call $contract $signature @arguments --rpc-url $rpc
        if ($LASTEXITCODE -ne 0) { throw 'Lecture on-chain échouée' }
        if (($value.Trim() -split ' ')[0] -ne $expected) { throw "Valeur inattendue : $signature $value, attendu $expected" }
    }
    Assert-Value $deployment.cashToken 'balanceOf(address)(uint256)' @($deployment.trader) '99996000000'
    Assert-Value $deployment.securityToken 'balanceOf(address)(uint256)' @($deployment.trader) '1000000000'
    Assert-Value $deployment.cashToken 'balanceOf(address)(uint256)' @($deployment.mm2) '99804921500'
    Assert-Value $deployment.securityToken 'balanceOf(address)(uint256)' @($deployment.mm2) '1100000000'
    Assert-Value $deployment.cashToken 'balanceOf(address)(uint256)' @($deployment.mm3) '100199078500'
    Assert-Value $deployment.securityToken 'balanceOf(address)(uint256)' @($deployment.mm3) '900000000'
    Assert-Value $deployment.cashToken 'totalSupply()(uint256)' @() '400000000000'
    Assert-Value $deployment.securityToken 'totalSupply()(uint256)' @() '4000000000'
    $buyId = cast keccak 'local-buy-100'
    $sellId = cast keccak 'local-sell-100'
    Assert-Value $deployment.settlement 'settled(address,address,bytes32)(bool)' @($deployment.trader, $deployment.mm2, $buyId) 'true'
    Assert-Value $deployment.settlement 'settled(address,address,bytes32)(bool)' @($deployment.mm3, $deployment.trader, $sellId) 'true'
    Write-Output 'OK : déploiement, financement, BUY et SELL signés, soldes exacts, supply et replay consommé.'
} finally {
    if ($null -ne $anvilProcess) {
        if (-not $anvilProcess.HasExited) { Stop-Process -Id $anvilProcess.Id }
        $anvilProcess.Dispose()
    }
    Pop-Location
}
