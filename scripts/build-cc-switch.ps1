[CmdletBinding()]
param(
    [ValidateSet('Prepare', 'Renderer', 'Test', 'Desktop')]
    [string]$Action = 'Prepare',
    [string]$OutputRoot = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$sourceRoot = Join-Path $repoRoot 'upstream/cc-switch'
$patchFile = Join-Path $repoRoot 'patches/cc-switch/0001-personal-proxy-integration.patch'
$expectedCommit = '7afceec14b977ecc2f4373e8ffb40e7223b435ac'

if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot '.git'))) {
    throw "CC Switch submodule is missing: $sourceRoot"
}
$actualCommit = (& git -C $sourceRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $expectedCommit) {
    throw "Unexpected CC Switch commit. Expected $expectedCommit, got $actualCommit"
}
if (-not (Test-Path -LiteralPath $patchFile)) {
    throw "Patch file is missing: $patchFile"
}

if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
    $OutputRoot = Join-Path $repoRoot '.build/cc-switch'
}
$outputRootResolved = [System.IO.Path]::GetFullPath($OutputRoot)
$buildRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot '.build'))
if (-not $outputRootResolved.StartsWith($buildRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputRoot must stay below .build: $outputRootResolved"
}
$worktree = Join-Path $outputRootResolved ('source-' + $expectedCommit.Substring(0, 12))

function Invoke-Checked([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$File failed with exit code $LASTEXITCODE" }
}

function New-PatchedWorktree {
    New-Item -ItemType Directory -Force -Path $outputRootResolved | Out-Null
    if (Test-Path -LiteralPath $worktree) {
        if (-not (Test-Path -LiteralPath (Join-Path $worktree '.git'))) {
            throw "Refusing to replace a directory that is not a generated build copy: $worktree"
        }
        Remove-Item -LiteralPath $worktree -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $worktree | Out-Null
    $archive = Join-Path $outputRootResolved 'cc-switch-source.tar'
    Invoke-Checked 'git' @('-C', $sourceRoot, 'archive', '--format=tar', '-o', $archive, $expectedCommit)
    Invoke-Checked 'tar' @('-xf', $archive, '-C', $worktree)
    Remove-Item -LiteralPath $archive -Force
    Invoke-Checked 'git' @('-C', $worktree, 'init', '--quiet')
    Invoke-Checked 'git' @('-C', $worktree, 'config', 'user.name', 'codex-build')
    Invoke-Checked 'git' @('-C', $worktree, 'config', 'user.email', 'codex-build@localhost')
    Invoke-Checked 'git' @('-C', $worktree, 'add', '.')
    Invoke-Checked 'git' @('-C', $worktree, 'commit', '--quiet', '-m', 'pinned CC Switch source')
    Invoke-Checked 'git' @('-C', $worktree, 'apply', '--check', $patchFile)
    Invoke-Checked 'git' @('-C', $worktree, 'apply', '--binary', $patchFile)
    Write-Output "Prepared patched source: $worktree"
}

$patchApplied = $false
if (Test-Path -LiteralPath (Join-Path $worktree '.git')) {
    try {
        & git -C $worktree apply --reverse --check $patchFile 2>$null
        $patchApplied = ($LASTEXITCODE -eq 0)
    } catch {
        $patchApplied = $false
    }
}
if ($Action -eq 'Prepare' -or -not $patchApplied) {
    New-PatchedWorktree
}

Push-Location $worktree
try {
    if ($Action -ne 'Prepare' -and -not (Test-Path -LiteralPath 'node_modules')) {
        Invoke-Checked 'pnpm' @('install', '--frozen-lockfile')
    }
    switch ($Action) {
        'Prepare' { }
        'Renderer' { Invoke-Checked 'pnpm' @('typecheck'); Invoke-Checked 'pnpm' @('build:renderer') }
        'Test' { Invoke-Checked 'pnpm' @('typecheck'); Invoke-Checked 'pnpm' @('exec', 'vitest', 'run', 'tests/components/PersonalProxyPage.test.tsx') }
        'Desktop' {
            if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) { throw 'cargo/rustc is required for Desktop.' }
            Invoke-Checked 'pnpm' @('typecheck')
            Invoke-Checked 'pnpm' @('tauri', 'build', '--no-bundle')
        }
    }
} finally {
    Pop-Location
}
