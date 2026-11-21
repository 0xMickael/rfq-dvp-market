# Every measured trial gets a fresh database, Anvil chain and application processes.
[CmdletBinding()]
param(
    [string]$ApiBinary = '',
    [string]$EngineBinary = '',
    [string]$LoadgenBinary = '',
    [ValidateRange(1,10)][int]$Repetitions = 3,
    [int[]]$Concurrency = @(1,8),
    [ValidateSet('quotes','accept','settlement')][string[]]$Modes = @('quotes','accept','settlement'),
    [ValidateRange(1,10000)][int]$Traders = 8,
    [ValidateRange(1,300)][int]$DurationSeconds = 3,
    [ValidateRange(0,30)][int]$WarmupSeconds = 1,
    [ValidateRange(1,10000)][int]$Requests = 120,
    [ValidateRange(1,10000)][int]$SettlementRequests = 24,
    [ValidateRange(0,100)][int]$AcceptPercent = 100,
    [string]$RunDirectory = ''
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
if (-not $ApiBinary) { $ApiBinary = Join-Path $root '.cache/durable-api.exe' }
if (-not $EngineBinary) { $EngineBinary = Join-Path $root '.cache/loadgen-rust/release/rfq-dvp-market-engine.exe' }
if (-not $LoadgenBinary) { $LoadgenBinary = Join-Path $root '.cache/loadgen.exe' }
if (-not $RunDirectory) { $RunDirectory = Join-Path $root ('.cache/performance/' + [Guid]::NewGuid().ToString('N')) }
$RunDirectory = [IO.Path]::GetFullPath($RunDirectory)
$cluster = Join-Path $RunDirectory 'postgres'
$bin = Join-Path $root '.cache/tools/postgresql-18.7/pgsql/bin'
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()
$saved = @{}
$pgStarted = $false
foreach ($key in @('Path','DATABASE_URL','MARKET_ID','ENGINE_ADDR','RFQ_TEST_MODE','RFQ_TEST_FAILPOINT','DEPLOYMENT_FILE','FOUNDRY_BROADCAST','FOUNDRY_CACHE_PATH')) {
    $saved[$key] = [Environment]::GetEnvironmentVariable($key,'Process')
}
function Stop-Trial {
    for ($i=$processes.Count-1; $i -ge 0; $i--) {
        $process = $processes[$i]
        try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } }
        finally { $process.Dispose() }
    }
    $processes.Clear()
}
function Start-TrialProcess([string]$Name,[string]$File,[string[]]$Arguments) {
    $quoted = @($Arguments | ForEach-Object { if ($_ -match '\s') { '"'+$_+'"' } else { $_ } })
    $options = @{
        FilePath=$File; WorkingDirectory=$root; WindowStyle='Hidden'; PassThru=$true
        RedirectStandardOutput=(Join-Path $trial "$Name.out.log")
        RedirectStandardError=(Join-Path $trial "$Name.err.log")
    }
    if ($quoted.Count) { $options.ArgumentList=$quoted }
    $process = Start-Process @options
    $null = $process.Handle
    $processes.Add($process)
    return $process
}
function Wait-Trial([scriptblock]$Check) {
    for ($i=0; $i -lt 150; $i++) {
        foreach ($process in $processes) { if ($process.HasExited) { throw "Process exited. Logs: $trial" } }
        try { if (& $Check) { return } } catch { }
        Start-Sleep -Milliseconds 200
    }
    throw "Startup timed out. Logs: $trial"
}
function Run-Client([string]$Name,[string[]]$Arguments) {
    $process = Start-TrialProcess $Name $LoadgenBinary $Arguments
    if (-not $process.WaitForExit(180000)) { throw "Loadgen timed out. Logs: $trial" }
    $code = $process.ExitCode
    $processes.Remove($process) | Out-Null
    $process.Dispose()
    if ($code -ne 0) {
        Get-Content -Encoding UTF8 (Join-Path $trial "$Name.err.log")
        throw "Loadgen failed (exit code $code). Report and logs: $trial"
    }
}
try {
    $env:Path += ';'+[Environment]::GetEnvironmentVariable('Path','Machine')+';'+[Environment]::GetEnvironmentVariable('Path','User')
    foreach ($value in $Concurrency) { if ($value -lt 1 -or $value -gt $Traders) { throw 'Concurrency must be between 1 and the number of traders.' } }
    foreach ($file in @($ApiBinary,$EngineBinary,$LoadgenBinary)) { if (-not (Test-Path -LiteralPath $file)) { throw "Missing binary: $file. Build with scripts/verify.ps1." } }
    foreach ($port in @(38080,38081,38545,55435)) {
        $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,$port)
        try { $probe.Server.ExclusiveAddressUse=$true; $probe.Start() } finally { $probe.Stop() }
    }
    if (Test-Path -LiteralPath $RunDirectory) { throw 'Choose a new campaign directory.' }
    New-Item -ItemType Directory -Path $RunDirectory | Out-Null
    $cpu = Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors
    $machine = Get-CimInstance Win32_ComputerSystem
    $metadata = [ordered]@{
        utc=[DateTime]::UtcNow.ToString('o'); cpu=@($cpu); memory_bytes=$machine.TotalPhysicalMemory
        windows=[Environment]::OSVersion.VersionString; go=(& go version); rust=(& rustc --version)
        node=(& node --version); foundry=(& anvil --version); postgres=(& "$bin/pg_ctl.exe" --version)
        repetitions=$Repetitions; concurrency=$Concurrency; traders=$Traders; modes=$Modes
        admission_seconds=$DurationSeconds; warmup_seconds=$WarmupSeconds
        requests=$Requests; settlement_requests=$SettlementRequests
        accept_percent=$AcceptPercent
        mining='Anvil automine, one block per transaction, no artificial block delay'
        git_head=(& git rev-parse HEAD); dirty=([bool](& git status --porcelain))
        api_sha256=(Get-FileHash -LiteralPath $ApiBinary -Algorithm SHA256).Hash
        engine_sha256=(Get-FileHash -LiteralPath $EngineBinary -Algorithm SHA256).Hash
        loadgen_sha256=(Get-FileHash -LiteralPath $LoadgenBinary -Algorithm SHA256).Hash
        engine_profile=$(if ($EngineBinary -match '[\\/]release[\\/]') { 'release' } else { 'custom_or_debug' })
    }
    [IO.File]::WriteAllText((Join-Path $RunDirectory 'machine.json'),($metadata | ConvertTo-Json -Depth 6),[Text.UTF8Encoding]::new($false))
    & "$PSScriptRoot/postgres.ps1" -Action Init -DataDirectory $cluster -Port 55435
    & "$PSScriptRoot/postgres.ps1" -Action Start -DataDirectory $cluster -Port 55435
    $pgStarted=$true
    $env:RFQ_TEST_MODE=$null
    $env:RFQ_TEST_FAILPOINT=$null
    $results = [Collections.Generic.List[object]]::new()
    $number=0
    foreach ($mode in $Modes) { foreach ($concurrent in $Concurrency) { for ($repeat=1; $repeat -le $Repetitions; $repeat++) {
        $number++
        $trial = Join-Path $RunDirectory "$mode-c$concurrent-r$repeat"
        New-Item -ItemType Directory -Path $trial | Out-Null
        Write-Host "Campaign $mode, concurrency $concurrent, repetition $repeat. Logs: $trial"
        try {
            $database = "trial_$number"
            & "$bin/createdb.exe" -h 127.0.0.1 -p 55435 -U rfq $database
            if ($LASTEXITCODE -ne 0) { throw 'Database creation failed.' }
            & "$bin/psql.exe" -h 127.0.0.1 -p 55435 -U rfq -d $database -v ON_ERROR_STOP=1 -f (Join-Path $root 'migrations/001_durability.sql') | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'Migration failed.' }
            $env:DATABASE_URL="postgresql://rfq@127.0.0.1:55435/${database}?sslmode=disable&connect_timeout=3"
            $null = Start-TrialProcess 'anvil' (Get-Command anvil).Source @('--host','127.0.0.1','--port','38545','--chain-id','31337','--hardfork','cancun','--silent')
            Wait-Trial { (Invoke-RestMethod http://127.0.0.1:38545 -Method Post -ContentType application/json -Body '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' -TimeoutSec 1).result -eq '0x7a69' }
            $env:DEPLOYMENT_FILE=(Join-Path $trial 'deployment.json').Replace('\','/')
            $env:FOUNDRY_BROADCAST=Join-Path $trial 'broadcast'
            $env:FOUNDRY_CACHE_PATH=Join-Path $trial 'forge-cache'
            $deploy = Start-TrialProcess 'deploy' (Get-Command forge).Source @('script','contracts/script/DeployLocal.s.sol:DeployLocal','--root','contracts','--out',(Join-Path $trial 'forge-out'),'--rpc-url','http://127.0.0.1:38545','--broadcast','--non-interactive')
            if (-not $deploy.WaitForExit(60000) -or $deploy.ExitCode -ne 0) { throw 'Test deployment failed.' }
            $processes.Remove($deploy) | Out-Null
            $deploy.Dispose()
            $env:MARKET_ID= & $ApiBinary -deployment $env:DEPLOYMENT_FILE -rpc http://127.0.0.1:38545 -print-market-id
            if ($LASTEXITCODE -ne 0) { throw 'Chain identity unavailable.' }
            $env:ENGINE_ADDR='127.0.0.1:38081'
            $null = Start-TrialProcess 'engine' $EngineBinary @()
            Wait-Trial { (Invoke-WebRequest -UseBasicParsing http://127.0.0.1:38081/market -TimeoutSec 1).StatusCode -eq 200 }
            $arguments=@('-deployment',$env:DEPLOYMENT_FILE,'-rpc','http://127.0.0.1:38545','-listen','127.0.0.1:38080','-engine','http://127.0.0.1:38081')
            if ($mode -eq 'accept') { $arguments += '-settlement-worker=false' }
            $null = Start-TrialProcess 'api' $ApiBinary $arguments
            Wait-Trial { (Invoke-WebRequest -UseBasicParsing http://127.0.0.1:38080/api/readiness -TimeoutSec 1).StatusCode -eq 200 }
            $common=@('-api','http://127.0.0.1:38080','-rpc','http://127.0.0.1:38545','-traders',"$Traders",'-concurrency',"$concurrent",'-seed','42')
            if ($mode -ne 'quotes') { Run-Client 'prepare' ($common + @('-prepare')) }
            $limit=$Requests
            $quantities=@('-min-quantity','0.01','-max-quantity','0.1')
            if ($mode -eq 'accept') { $quantities=@('-min-quantity','600','-max-quantity','700') }
            if ($mode -eq 'settlement') { $limit=$SettlementRequests }
            $report=Join-Path $trial 'report.json'
            Run-Client 'loadgen' ($common + @('-mode',$mode,'-duration',"${DurationSeconds}s",'-warmup',"${WarmupSeconds}s",'-requests',"$limit",'-accept-percent',"$AcceptPercent",'-output',$report) + $quantities)
            $result=Get-Content -Raw -Encoding UTF8 $report | ConvertFrom-Json
            $results.Add([pscustomobject]@{mode=$mode;concurrency=$concurrent;repetition=$repeat;report=$result})
            foreach ($endpoint in @(@('go','http://127.0.0.1:38080/metrics'),@('rust','http://127.0.0.1:38081/metrics'))) {
                $text=(Invoke-WebRequest -UseBasicParsing $endpoint[1] -TimeoutSec 3).Content
                if ($text -notmatch '_http_requests_total' -or $text -match 'rfq-\d|quote-\d|trade_id=') { throw 'HTTP metrics missing or labels not bounded.' }
                [IO.File]::WriteAllText((Join-Path $trial ($endpoint[0]+'.prom')),$text,[Text.UTF8Encoding]::new($false))
            }
            Write-Host ("OK : {0} operations, {1:N2} ops/s, invariants={2}" -f $result.operations_completed,$result.completed_operations_per_second_including_drain,$result.invariants.passed)
        } finally { Stop-Trial }
    } } }
    [IO.File]::WriteAllText((Join-Path $RunDirectory 'results.json'),(@($results.ToArray()) | ConvertTo-Json -Depth 20),[Text.UTF8Encoding]::new($false))
    Write-Host "CAMPAIGNS PASSED: $RunDirectory"
} finally {
    Stop-Trial
    if ($pgStarted) { & "$PSScriptRoot/postgres.ps1" -Action Stop -DataDirectory $cluster -Port 55435 }
    foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key,$saved[$key],'Process') }
}
