[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$VerifyOnly,
    [string]$ExpectedCommit = '48e3b0dedbe523c2915473017d2595c34d2df7da'
)

$ErrorActionPreference = 'Stop'
if (-not $Build -and -not $VerifyOnly) {
    throw 'Specify -VerifyOnly or -Build. This script never enables or restores system proxy settings.'
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$sourceRoot = Join-Path $repoRoot 'third_party/chatgpt-proxy-manager'
if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot '.git'))) {
    throw "Submodule is missing: $sourceRoot"
}

$actualCommit = (& git -C $sourceRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $ExpectedCommit) {
    throw "Unexpected chatgpt-proxy-manager commit. Expected $ExpectedCommit, got $actualCommit"
}

$buildScript = Join-Path $sourceRoot 'build/build.ps1'
if (-not (Test-Path -LiteralPath $buildScript)) {
    throw "Upstream build script is missing: $buildScript"
}

$csc = Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$required = @($csc, (Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319\WPF\PresentationFramework.dll'))
$missing = @($required | Where-Object { -not (Test-Path -LiteralPath $_) })
if ($missing.Count -gt 0) {
    throw ('Required Windows build tools are missing: ' + ($missing -join ', '))
}

if ($VerifyOnly) {
    Write-Output "Verified source commit $actualCommit and build prerequisites. No upstream code or system settings were changed."
    exit 0
}

$status = (& git -C $sourceRoot status --porcelain)
if ($status) {
    throw 'Refusing to build a dirty upstream worktree; use a clean submodule checkout.'
}

& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $buildScript
if ($LASTEXITCODE -ne 0) {
    throw "Upstream build failed with exit code $LASTEXITCODE"
}

$output = Join-Path $sourceRoot 'dist/ChatGPTProxyManager.exe'
if (-not (Test-Path -LiteralPath $output)) {
    throw "Expected build output is missing: $output"
}
Get-FileHash -LiteralPath $output -Algorithm SHA256
Write-Output 'Build completed. The generated executable was not launched by this script.'
