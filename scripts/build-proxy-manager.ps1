[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$VerifyOnly,
    [string]$ExpectedCommit = '48e3b0dedbe523c2915473017d2595c34d2df7da',
    [string]$OutputRoot = ''
)

$ErrorActionPreference = 'Stop'
if (-not $Build -and -not $VerifyOnly) {
    throw 'Specify -VerifyOnly or -Build. This script never enables or restores system proxy settings.'
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$sourceRoot = Join-Path $repoRoot 'third_party/chatgpt-proxy-manager'
$patchRoot = Join-Path $repoRoot 'patches/proxy-manager'
$prepare = Join-Path $repoRoot 'scripts/prepare-proxy-manager.py'
if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot '.git'))) { throw "Submodule is missing: $sourceRoot" }
if (-not (Test-Path -LiteralPath $prepare) -or -not (Test-Path -LiteralPath (Join-Path $patchRoot 'patch.py'))) { throw 'Isolated proxy-manager patch files are missing.' }

$actualCommit = (& git -C $sourceRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $ExpectedCommit) { throw "Unexpected chatgpt-proxy-manager commit. Expected $ExpectedCommit, got $actualCommit" }
if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot 'build/build.ps1'))) { throw 'Upstream build script is missing.' }

$fw = Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319'
$required = @(
    (Join-Path $fw 'csc.exe'),
    (Join-Path $fw 'WPF\PresentationFramework.dll'),
    (Join-Path $fw 'System.Security.dll'),
    (Join-Path $fw 'System.Web.Extensions.dll'))
$missing = @($required | Where-Object { -not (Test-Path -LiteralPath $_) })
if ($missing.Count -gt 0) { throw ('Required Windows build tools are missing: ' + ($missing -join ', ')) }
if ($VerifyOnly) {
    Write-Output "Verified source commit $actualCommit, isolated patch files, and build prerequisites. No source or system settings were changed."
    exit 0
}

if ([string]::IsNullOrWhiteSpace($OutputRoot)) { $OutputRoot = Join-Path $repoRoot '.build/proxy-manager' }
$outputRootResolved = [System.IO.Path]::GetFullPath($OutputRoot)
$repoPrefix = $repoRoot.TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
if (-not $outputRootResolved.StartsWith($repoPrefix, [System.StringComparison]::OrdinalIgnoreCase)) { throw "OutputRoot must be inside the repository: $outputRootResolved" }
$isolation = Join-Path $outputRootResolved ('source-' + $actualCommit.Substring(0, 12))
$python = Get-Command python -ErrorAction SilentlyContinue
if ($null -eq $python) { throw 'Python 3 is required to export and patch the isolated source.' }

& $python.Source $prepare $sourceRoot $isolation $ExpectedCommit
if ($LASTEXITCODE -ne 0) { throw "Isolated source preparation failed with exit code $LASTEXITCODE" }
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $isolation 'build/build.ps1')
if ($LASTEXITCODE -ne 0) { throw "Proxy-manager build failed with exit code $LASTEXITCODE" }

$output = Join-Path $isolation 'dist/ChatGPTProxyManager.exe'
if (-not (Test-Path -LiteralPath $output)) { throw "Expected build output is missing: $output" }
$hash = Get-FileHash -LiteralPath $output -Algorithm SHA256
Write-Output ("Build completed from isolated source: " + $output)
Write-Output ("SHA256: " + $hash.Hash)
Write-Output 'The generated executable was not launched by this script.'
