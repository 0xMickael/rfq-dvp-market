$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$processes = @()
try {
    foreach ($port in @(8080, 8081, 5173)) {
        if (Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue) {
            throw "Port $port occupé : aucun service existant ne sera arrêté."
        }
    }
    $processes += Start-Process -FilePath (Join-Path $root '.cache/stage1-rust/debug/rfq-dvp-market-engine.exe') -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath (Join-Path $root '.cache/signatures-api.exe') -ArgumentList @('-deployment', 'testdata/signing-deployment.json') -WorkingDirectory (Join-Path $root 'backend') -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath (Get-Command node).Source -ArgumentList @('node_modules/vite/bin/vite.js', 'preview', '--host', '127.0.0.1', '--port', '5173', '--strictPort') -WorkingDirectory (Join-Path $root 'web') -WindowStyle Hidden -PassThru
    foreach ($url in @('http://127.0.0.1:8081/healthz', 'http://127.0.0.1:8080/healthz', 'http://127.0.0.1:5173')) {
        $ready = $false
        for ($i = 0; $i -lt 40; $i++) {
            try { $null = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 2; $ready = $true; break } catch { }
            Start-Sleep -Milliseconds 250
        }
        if (-not $ready) { throw "Service indisponible : $url" }
    }
    & node (Join-Path $root 'web/tests/live-signatures.mjs')
    if ($LASTEXITCODE -ne 0) { throw 'Échec du test des signatures.' }
} finally {
    foreach ($process in $processes) {
        if (-not $process.HasExited) { Stop-Process -Id $process.Id }
        $process.Dispose()
    }
}
