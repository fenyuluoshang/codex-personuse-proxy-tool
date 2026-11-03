[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$OutputRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('CodexPersonalProxyTool.WindowsRecovery.Tests.' + [Guid]::NewGuid().ToString('N'))
$fw = Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319'
$csc = Join-Path $fw 'csc.exe'
if (-not (Test-Path -LiteralPath $csc)) { throw "Missing compiler: $csc" }
New-Item -ItemType Directory -Path $OutputRoot | Out-Null
$out = Join-Path $OutputRoot 'RecoveryTests.exe'
$journal = Join-Path $scriptRoot '..\..\patches\proxy-manager\RecoveryJournal.cs'
$refs = @(
    (Join-Path $fw 'System.dll'),
    (Join-Path $fw 'System.Core.dll'),
    (Join-Path $fw 'System.Xml.dll'),
    (Join-Path $fw 'System.Security.dll')
)
& $csc /nologo /target:exe /platform:anycpu /optimize+ /codepage:65001 `
    ("/out:" + $out) ("/reference:" + ($refs -join ';')) `
    $journal (Join-Path $scriptRoot 'RecoveryTests.cs')
if ($LASTEXITCODE -ne 0) { throw "Recovery test compile failed: $LASTEXITCODE" }
try {
    & $out
    if ($LASTEXITCODE -ne 0) { throw "Recovery tests failed: $LASTEXITCODE" }
}
finally {
    if (Test-Path -LiteralPath $OutputRoot) { Remove-Item -LiteralPath $OutputRoot -Recurse -Force }
}
