# Download and run this file with -File when passing parameters; do not use iex.
# The bootstrap installs only the CLI and never silently updates an existing one.
[CmdletBinding()]
param(
    [string]$Project = '',
    [string]$Agents = '',
    [switch]$DryRun,
    [string]$InstallDir = (Join-Path $env:USERPROFILE '.onebase\bin'),
    [string]$ReleaseApi = 'https://api.github.com/repos/ivanarama/onebase/releases/latest'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if ($Agents -and -not $Project) { throw '-Agents requires -Project' }
if ($Agents) {
    foreach ($agent in $Agents.Split(',')) {
        if ($agent -cnotin @('claude', 'cursor', 'opencode', 'zcode')) { throw "Unknown agent: $agent" }
    }
}
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
if ($Project) { $Project = [IO.Path]::GetFullPath($Project) }
if ($DryRun) {
    Write-Output "Dry-run: keep existing onebase, otherwise install CLI at $InstallDir"
    if ($Project) { Write-Output "Dry-run: vibe-init project=$Project agents=$Agents (auto if empty)" }
    return
}
function Assert-NotLink([string]$Path) {
    if (Test-Path -LiteralPath $Path) {
        $item = Get-Item -LiteralPath $Path -Force
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Symlink/reparse point: $Path" }
    }
}
Assert-NotLink $InstallDir
$exe = Join-Path $InstallDir 'onebase.exe'
Assert-NotLink $exe
if (Test-Path -LiteralPath $exe) {
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw 'Installed binary is not a file' }
} else {
    $existing = Get-Command onebase -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($existing) { $exe = $existing.Source; Assert-NotLink $exe } else { $exe = '' }
}
function Test-Binary([string]$Path) {
    & $Path --version
    if ($LASTEXITCODE -ne 0) { throw 'Binary failed --version; existing installation preserved' }
    if ($Project) {
        & $Path vibe-init --help | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'This binary has no usable vibe-init. Update explicitly to a supporting release.' }
    }
}
$staging = ''
$publish = ''
try {
    if ($exe) {
        Write-Output "Keeping existing binary: $exe"
        Test-Binary $exe
    } else {
        $arch = $env:PROCESSOR_ARCHITEW6432
        if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
        switch ($arch) {
            'AMD64' { $goarch = 'amd64' }
            'ARM64' { $goarch = 'arm64' }
            default { throw "Unsupported Windows architecture: $arch" }
        }
        $assetName = "onebase-windows-$goarch.zip"
        $staging = Join-Path ([IO.Path]::GetTempPath()) ('onebase-install-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $staging | Out-Null
        $release = Invoke-RestMethod -Uri $ReleaseApi
        function Get-AssetUrl([string]$Name) {
            $assets = @($release.assets | Where-Object { $_.name -ceq $Name })
            if ($assets.Count -ne 1 -or -not $assets[0].browser_download_url) { throw "Missing or duplicate exact asset: $Name" }
            return $assets[0].browser_download_url
        }
        $archiveUrl = Get-AssetUrl $assetName
        $sumUrl = Get-AssetUrl "$assetName.sha256"
        $archivePath = Join-Path $staging $assetName
        $sumPath = Join-Path $staging 'sum'
        Invoke-WebRequest -UseBasicParsing -Uri $archiveUrl -OutFile $archivePath
        Invoke-WebRequest -UseBasicParsing -Uri $sumUrl -OutFile $sumPath
        $sum = [IO.File]::ReadAllText($sumPath, [Text.Encoding]::UTF8).Trim()
        if ($sum -notmatch ('^([0-9a-fA-F]{64})\s+' + [regex]::Escape($assetName) + '$')) { throw 'Invalid checksum record' }
        $expected = $Matches[1].ToLowerInvariant()
        $hasher = [Security.Cryptography.SHA256]::Create()
        try {
            $hashInput = [IO.File]::OpenRead($archivePath)
            try {
                $actual = [BitConverter]::ToString($hasher.ComputeHash($hashInput)).Replace('-', '').ToLowerInvariant()
            } finally { $hashInput.Dispose() }
        } finally { $hasher.Dispose() }
        if ($actual -cne $expected) { throw 'SHA-256 mismatch' }
        # Windows release ZIPs are flat. Stream only the exact CLI entry,
        # never expand archive-controlled paths or select a recursive match.
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $zip = [IO.Compression.ZipFile]::OpenRead($archivePath)
        try {
            $entries = @($zip.Entries | Where-Object { $_.FullName -ceq 'onebase.exe' })
            if ($entries.Count -ne 1) { throw 'Archive must contain exactly one platform CLI' }
            $stagedExe = Join-Path $staging 'onebase.exe'
            $source = $entries[0].Open()
            try {
                $target = [IO.File]::Open($stagedExe, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
                try { $source.CopyTo($target); $target.Flush($true) } finally { $target.Dispose() }
            } finally { $source.Dispose() }
        } finally { $zip.Dispose() }
        Test-Binary $stagedExe
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        Assert-NotLink $InstallDir
        $publish = Join-Path $InstallDir ('.onebase-publish-' + [Guid]::NewGuid().ToString('N'))
        # Same-directory complete-file publication. Move refuses an existing
        # target, including a concurrent installation; no delete-then-rename.
        [IO.File]::Copy($stagedExe, $publish, $false)
        $exe = Join-Path $InstallDir 'onebase.exe'
        [IO.File]::Move($publish, $exe)
        $publish = ''
        Write-Output "Installed CLI: $exe"
    }
    if ($Project) {
        $initArgs = @('vibe-init', $Project)
        if ($Agents) { $initArgs += @('--agents', $Agents) }
        & $exe @initArgs
        if ($LASTEXITCODE -ne 0) { throw "vibe-init failed (exit $LASTEXITCODE); inspect its report before retrying" }
    } else {
        Write-Output "Run: & '$exe' start"
    }
} finally {
    if ($publish -and (Test-Path -LiteralPath $publish)) { Remove-Item -LiteralPath $publish -Force }
    if ($staging -and (Test-Path -LiteralPath $staging)) { Remove-Item -LiteralPath $staging -Recurse -Force }
}
