# Local demonstration/test cluster only. Trust authentication is restricted to loopback.
[CmdletBinding()]
param(
    [ValidateSet('Init','Start','Stop','Migrate')][string]$Action = 'Start',
    [string]$DataDirectory = '',
    [ValidateRange(1024,65535)][int]$Port = 55432
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
if (-not $DataDirectory) { $DataDirectory = Join-Path $root '.cache/postgres/demo' }
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)
$bin = Join-Path $root '.cache/tools/postgresql-18.7/pgsql/bin'
if (-not (Test-Path "$bin/pg_ctl.exe")) { throw 'Run scripts/install-postgres.ps1 first.' }
function Checked([string]$File,[string[]]$Arguments) {
    & "$bin/$File.exe" @Arguments
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL: $File failed." }
}
switch ($Action) {
    'Init' {
        if (Test-Path "$DataDirectory/PG_VERSION") { throw 'Cluster already exists; no automatic reset.' }
        New-Item -ItemType Directory -Force (Split-Path $DataDirectory) | Out-Null
        Checked 'initdb' @('-D',$DataDirectory,'-U','rfq','-A','trust','--encoding=UTF8','--no-locale')
        # Only IPv4 loopback; never an external interface.
        [IO.File]::WriteAllText("$DataDirectory/pg_hba.conf", "host all all 127.0.0.1/32 trust`n",[Text.UTF8Encoding]::new($false))
    }
    'Start' {
        if (-not (Test-Path "$DataDirectory/PG_VERSION")) { throw 'Initialize the cluster explicitly with -Action Init.' }
        Checked 'pg_ctl' @('-D',$DataDirectory,'-l',"$DataDirectory/server.log",'-o',"-h 127.0.0.1 -p $Port",'-w','start')
    }
    'Stop' { Checked 'pg_ctl' @('-D',$DataDirectory,'-m','fast','-w','stop') }
    'Migrate' {
        # postgres is a dedicated database within this project-only cluster.
        Checked 'psql' @('-h','127.0.0.1','-p',"$Port",'-U','rfq','-d','postgres','-v','ON_ERROR_STOP=1','-f',(Join-Path $root 'migrations/001_durability.sql'))
    }
}
