# onebase installer for Windows
# Usage: irm https://raw.githubusercontent.com/ivanarama/onebase/main/install.ps1 | iex
#
# Безопасность установки (issue #781):
#   * ассеты выбираются по ТОЧНЫМ именам, а не по wildcard, — так нельзя
#     случайно скачать файл контрольной суммы вместо архива;
#   * SHA-256 архива проверяется по опубликованной рядом сумме ДО распаковки —
#     битая или подменённая на пути закачка отклоняется;
#   * распаковка и проверка идут в отдельной staging-директории, и рабочий
#     бинарь заменяется только после того, как новый распакован и запущен
#     с --version;
#   * staging гарантированно убирается (в т.ч. при ошибке).

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo       = "ivanarama/onebase"
$installDir = "$env:USERPROFILE\.onebase\bin"
# Имена ассетов совпадают с теми, что публикует release.yml
# ($name = "onebase-$goos-$goarch"): для целевой платформы они стабильны и не
# зависят от версии.
$zipName    = "onebase-windows-amd64.zip"
$shaName    = "$zipName.sha256"

Write-Host "onebase installer" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan

# --- Latest release -----------------------------------------------------------
Write-Host "Checking latest release..."
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$version = $release.tag_name
Write-Host "Latest version: $version"

function Get-AssetUrl($name) {
    $asset = $release.assets | Where-Object { $_.name -eq $name } | Select-Object -First 1
    if (-not $asset) {
        throw "В релизе $version нет ожидаемого файла '$name'. Установка прервана."
    }
    return $asset.browser_download_url
}

$zipUrl = Get-AssetUrl $zipName
$shaUrl = Get-AssetUrl $shaName

# --- Staging ------------------------------------------------------------------
# Отдельная временная директория: скачиваем, проверяем и распаковываем ЗДЕСЬ, и
# только при полном успехе переносим результат в installDir. try/finally
# гарантирует уборку staging даже при ошибке проверки.
$staging = Join-Path $env:TEMP ("onebase-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $staging | Out-Null

try {
    $zipPath = Join-Path $staging $zipName
    $shaPath = Join-Path $staging $shaName

    Write-Host "Downloading $zipName..."
    Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath
    Invoke-WebRequest -Uri $shaUrl -OutFile $shaPath

    # --- Checksum verification (ДО распаковки) --------------------------------
    # Формат .sha256: "<hex>  <имя_файла>" (как sha256sum / Get-FileHash-скрипт
    # в release.yml). Берём первый токен.
    $shaLine  = (Get-Content -Path $shaPath -Raw).Trim()
    $expected = ($shaLine -split '\s+')[0].ToLower()
    if ($expected -notmatch '^[0-9a-f]{64}$') {
        throw "Файл контрольной суммы '$shaName' имеет неожиданный формат. Установка прервана."
    }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToLower()
    if ($actual -ne $expected) {
        throw "Контрольная сумма не совпала для $zipName`n  ожидалось: $expected`n  получено:  $actual`nАрхив повреждён или подменён. Установка прервана."
    }
    Write-Host "Checksum OK (SHA-256)" -ForegroundColor Green

    # --- Extract + verify binary ----------------------------------------------
    $extractDir = Join-Path $staging "extract"
    Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force

    $stagedExe = Get-ChildItem -Path $extractDir -Filter "onebase.exe" -Recurse | Select-Object -First 1
    if (-not $stagedExe) {
        throw "В архиве $zipName не найден onebase.exe. Установка прервана."
    }

    # Проверяем, что распакованный бинарь вообще запускается, — до замены
    # рабочего. Если новый файл нерабочий, прежняя установка останется целой.
    Write-Host "Verifying downloaded binary..."
    & $stagedExe.FullName --version | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Скачанный onebase.exe не прошёл проверку '--version' (код $LASTEXITCODE). Установка прервана."
    }

    # --- Install (замена только после успешной проверки) ----------------------
    Write-Host "Installing to $installDir..."
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    # Переносим содержимое каталога с бинарём (onebase.exe, onebase-gui.exe,
    # README, examples) в installDir, замещая прежние файлы. Копируем из
    # каталога, где лежит проверенный onebase.exe.
    $payloadDir = $stagedExe.Directory.FullName
    Copy-Item -Path (Join-Path $payloadDir '*') -Destination $installDir -Recurse -Force

    $exe = Join-Path $installDir "onebase.exe"
    if (-not (Test-Path $exe)) {
        throw "Не удалось установить onebase.exe в $installDir. Установка прервана."
    }
}
finally {
    # Staging убираем всегда: и при успехе, и при любой ошибке выше.
    if (Test-Path $staging) {
        Remove-Item -Path $staging -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# --- PATH ---------------------------------------------------------------------
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
