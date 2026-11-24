<#
.SYNOPSIS
Manages a standalone Codex CLI sign-in profile on Windows.
.EXAMPLE
.\scripts\codex-sign-in.ps1 login
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('login', 'status', 'logout')]
    [string]$Action = 'status'
)

$ErrorActionPreference = 'Stop'
$codex = Get-Command codex -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $codex) {
    throw 'Install the Codex CLI and make sure codex is on PATH.'
}
if (-not $env:LOCALAPPDATA) {
    throw 'LOCALAPPDATA is required for the standalone Codex profile.'
}

$profilePath = Join-Path $env:LOCALAPPDATA 'ARTEX\codex-sign-in'
[void][System.IO.Directory]::CreateDirectory($profilePath)
$config = @('-c', 'cli_auth_credentials_store=file')
[string[]]$command = switch ($Action) {
    'login' { @('login', '--device-auth') }
    'status' { @('login', 'status') }
    'logout' { @('logout') }
}

$savedEnvironment = @{}
$environmentNames = @('CODEX_HOME', 'OPENAI_API_KEY', 'CODEX_API_KEY', 'CODEX_ACCESS_TOKEN')
foreach ($name in $environmentNames) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

try {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
    $env:CODEX_HOME = $profilePath
    & $codex.Source @command @config
    $exitCode = $LASTEXITCODE
} finally {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
}

exit $exitCode
