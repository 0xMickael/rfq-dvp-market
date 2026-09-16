$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
$processes = @()
$client = $null
try {
    foreach ($port in @(8080, 8081, 5173)) {
        if (Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue) {
            throw "Port $port occupé : arrêter manuellement le service avant ce test."
        }
    }
    $engine = Start-Process -FilePath (Join-Path $root '.cache/stage1-rust/debug/rfq-dvp-market-engine.exe') -WindowStyle Hidden -PassThru
    $processes += $engine
    $processes += Start-Process -FilePath (Join-Path $root '.cache/quotes-api.exe') -WindowStyle Hidden -PassThru
    $processes += Start-Process -FilePath (Get-Command node).Source -ArgumentList @('node_modules/vite/bin/vite.js', 'preview', '--host', '127.0.0.1', '--port', '5173', '--strictPort') -WorkingDirectory (Join-Path $root 'web') -WindowStyle Hidden -PassThru
    Add-Type -AssemblyName System.Net.Http
    $client = New-Object System.Net.Http.HttpClient
    $client.Timeout = [TimeSpan]::FromSeconds(5)
    foreach ($url in @('http://127.0.0.1:8081/healthz', 'http://127.0.0.1:8080/healthz', 'http://127.0.0.1:5173')) {
        $ready = $false
        for ($i = 0; $i -lt 40; $i++) {
            try {
                $response = $client.GetAsync($url).GetAwaiter().GetResult()
                $ready = $response.IsSuccessStatusCode
                $response.Dispose()
                if ($ready) { break }
            } catch { }
            Start-Sleep -Milliseconds 250
        }
        if (-not $ready) { throw "Service indisponible : $url" }
    }
    function Quote($side, $unit, $amount, $expectedStatus) {
        $json = @{ side = $side; unit = $unit; amount = $amount } | ConvertTo-Json -Compress
        $content = New-Object System.Net.Http.StringContent($json, [System.Text.Encoding]::UTF8, 'application/json')
        try {
            $response = $client.PostAsync('http://127.0.0.1:5173/api/quotes', $content).GetAwaiter().GetResult()
            try {
                $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
                if ([int]$response.StatusCode -ne $expectedStatus) { throw "Status inattendu : $($response.StatusCode) $body" }
                return ($body | ConvertFrom-Json)
            } finally { $response.Dispose() }
        } finally { $content.Dispose() }
    }
    foreach ($side in @('BUY', 'SELL')) {
        foreach ($unit in @('tBILL', 'sEUR')) {
            $book = Quote $side $unit '100' 200
            $best = if ($side -eq 'BUY') { 'MM2' } else { 'MM3' }
            if ($book.quotes.Count -ne 3 -or $book.quotes[0].maker_id -ne $best -or $book.quotes[0].cash_amount -isnot [string]) { throw 'Quotes incorrectes' }
        }
    }
    $book = Quote 'BUY' 'tBILL' '1100' 200
    if ($book.quotes.Count -ne 1 -or $book.rejections.Count -ne 2) { throw 'Refus partiels par MM incorrects' }
    $book = Quote 'SELL' 'tBILL' '100000' 422
    if ($book.code -ne 'NO_EXECUTABLE_QUOTE' -or $book.rejections.Count -ne 3) { throw 'Refus global incorrect' }
    $book = Quote 'BUY' 'tBILL' '0.0000001' 400
    if ($book.code -ne 'INVALID_AMOUNT') { throw 'Précision invalide acceptée' }
    Stop-Process -Id $engine.Id
    $engine.WaitForExit()
    $book = Quote 'BUY' 'tBILL' '100' 503
    if ($book.code -ne 'ENGINE_UNAVAILABLE') { throw 'Panne moteur incorrectement signalée' }
    Write-Output 'OK : Vite -> Go -> Rust, quatre modes, refus, précision et moteur indisponible.'
} finally {
    if ($null -ne $client) { $client.Dispose() }
    foreach ($process in $processes) {
        if (-not $process.HasExited) { Stop-Process -Id $process.Id }
        $process.Dispose()
    }
}
