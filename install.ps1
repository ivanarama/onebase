# onebase installer for Windows
# Usage: irm https://raw.githubusercontent.com/ivantit66/onebase/main/install.ps1 | iex

$ErrorActionPreference = "Stop"

$repo = "ivantit66/onebase"
$installDir = "$env:USERPROFILE\.onebase\bin"
$exe = "$installDir\onebase.exe"

Write-Host "onebase installer" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan

# Get latest release
Write-Host "Checking latest release..."
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$version = $release.tag_name
Write-Host "Latest version: $version"

# Find Windows asset
$asset = $release.assets | Where-Object { $_.name -like "*windows*amd64*" } | Select-Object -First 1
if (-not $asset) {
    Write-Error "No Windows binary found in release $version"
    exit 1
}

# Download
Write-Host "Downloading $($asset.name)..."
$tmpZip = [System.IO.Path]::GetTempFileName() + ".zip"
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmpZip

# Install
Write-Host "Installing to $installDir..."
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Expand-Archive -Path $tmpZip -DestinationPath $installDir -Force

# Move exe if it's in a subdirectory
$innerExe = Get-ChildItem -Path $installDir -Filter "onebase.exe" -Recurse | Select-Object -First 1
if ($innerExe.FullName -ne $exe) {
    Move-Item $innerExe.FullName $exe -Force
}
Remove-Item $tmpZip -ErrorAction SilentlyContinue

# Add to PATH
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$installDir*") {
    Write-Host "Adding $installDir to PATH..."
    [Environment]::SetEnvironmentVariable("PATH", "$userPath;$installDir", "User")
    $env:PATH += ";$installDir"
}

Write-Host ""
Write-Host "Установка завершена!" -ForegroundColor Green
Write-Host "Запустите: onebase start" -ForegroundColor Yellow
Write-Host "(Если команда не найдена, перезапустите терминал)" -ForegroundColor Gray
