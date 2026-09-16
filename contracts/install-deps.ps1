$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    forge install --root . --no-git OpenZeppelin/openzeppelin-contracts@rev=c64a1edb67b6e3f4a15cca8909c9482ad33a02b0
    if ($LASTEXITCODE -ne 0) { throw 'Installation OpenZeppelin échouée' }
    forge install --root . --no-git foundry-rs/forge-std@rev=77041d2ce690e692d6e03cc812b57d1ddaa4d505
    if ($LASTEXITCODE -ne 0) { throw 'Installation forge-std échouée' }
} finally { Pop-Location }
