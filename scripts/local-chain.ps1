# Keep this chain running while restarting Go/Rust. Initialization is explicit.
[CmdletBinding()]
param([switch]$Initialize,[ValidateRange(0,86400)][int]$RunSeconds=0)
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot
$folder=Join-Path $root '.cache/local-market'
$manifest=Join-Path $folder 'deployment.json'
$state=Join-Path $folder 'anvil.json'
$saved=@{}
foreach($key in @('Path','DEPLOYMENT_FILE','FOUNDRY_BROADCAST','FOUNDRY_CACHE_PATH')){$saved[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
$process=$null
try{
    $env:Path+=';'+[Environment]::GetEnvironmentVariable('Path','Machine')+';'+[Environment]::GetEnvironmentVariable('Path','User')
    if($Initialize -and ((Test-Path $manifest) -or (Test-Path $state))){throw 'Local market already exists; no automatic redeployment or reset.'}
    if(-not $Initialize -and (-not(Test-Path $manifest) -or -not(Test-Path $state))){throw 'Initialize once with -Initialize.'}
    $probe=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,8545)
    try{$probe.Server.ExclusiveAddressUse=$true;$probe.Start()}finally{$probe.Stop()}
    New-Item -ItemType Directory -Force $folder | Out-Null
    $process=Start-Process -FilePath (Get-Command anvil).Source -ArgumentList @('--host','127.0.0.1','--port','8545','--chain-id','31337','--hardfork','cancun','--block-time','1','--state',('"'+$state+'"'),'--state-interval','1','--preserve-historical-states','--silent') -WindowStyle Hidden -PassThru -RedirectStandardOutput "$folder/anvil.out.log" -RedirectStandardError "$folder/anvil.err.log"
    $null=$process.Handle
    $ready=$false
    for($i=0;$i -lt 100;$i++){
        if($process.HasExited){throw 'Anvil exited; check the logs.'}
        try{$r=Invoke-RestMethod 'http://127.0.0.1:8545' -Method Post -ContentType 'application/json' -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 1;if($r.result -eq '0x7a69'){$ready=$true;break}}catch{}
        Start-Sleep -Milliseconds 200
    }
    if(-not $ready){throw 'Anvil unavailable.'}
    if($Initialize){
        $env:DEPLOYMENT_FILE=$manifest.Replace('\','/')
        $env:FOUNDRY_BROADCAST=Join-Path $folder 'broadcast'
        $env:FOUNDRY_CACHE_PATH=Join-Path $folder 'forge-cache'
        Push-Location (Join-Path $root 'contracts')
        try{& forge script script/DeployLocal.s.sol:DeployLocal --rpc-url http://127.0.0.1:8545 --broadcast --non-interactive;if($LASTEXITCODE -ne 0){throw 'Market initialization failed.'}}finally{Pop-Location}
        Start-Sleep -Seconds 2
    }
    Write-Host 'Local chain ready. Run scripts/start.ps1 in another terminal. Keep this chain running during Go/Rust restarts.'
    $began=[DateTime]::UtcNow
    while($RunSeconds -eq 0 -or ([DateTime]::UtcNow-$began).TotalSeconds -lt $RunSeconds){if($process.HasExited){throw 'Anvil exited.'};Start-Sleep -Milliseconds 300}
}finally{
    if($null -ne $process){if(-not $process.HasExited){$process.Kill();$process.WaitForExit()};$process.Dispose()}
    foreach($key in $saved.Keys){[Environment]::SetEnvironmentVariable($key,$saved[$key],'Process')}
}
