# Сборка всех программ FI и установщика.
#   powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Version 0.1.0]
# Автообновление приложения (без этих параметров выключено):
#   -UpdateURL https://…/update.json -PublicKey <base64>          вшить адрес и открытый ключ
#   -SignKey keys\update.key -InstallerURL https://…/setup.exe    подписать build\update.json
# Ключи создаёт: go run ./cmd/fi-sign keygen keys\update.key
param(
    [string]$Version = "0.1.0",
    [string]$UpdateURL = "",
    [string]$PublicKey = "",
    [string]$SignKey = "",
    [string]$InstallerURL = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
New-Item -ItemType Directory -Force build | Out-Null

function Invoke-Go([string[]]$GoArgs) {
    & go @GoArgs
    if ($LASTEXITCODE -ne 0) { throw "go $($GoArgs -join ' ') завершился с ошибкой" }
}

function Build([string]$Output, [string]$Package, [string]$Flags = "", [string]$Tags = "") {
    Write-Host "→ $Output"
    $goArgs = @("build", "-trimpath", "-ldflags", "-s -w -X main.version=$Version $Flags".Trim(), "-o", "build\$Output")
    if ($Tags) { $goArgs += @("-tags", $Tags) }
    Invoke-Go ($goArgs + $Package)
}

# Значок, манифест и сведения о версии попадают в .exe через файлы .syso рядом с main.go.
Write-Host "→ значок и ресурсы Windows"
Invoke-Go @("run", "./cmd/fi-res", "-version", $Version)

$updateFlags = ""
if ($UpdateURL -and $PublicKey) {
    $updateFlags = "-X fi/internal/appupdate.ManifestURL=$UpdateURL -X fi/internal/appupdate.PublicKey=$PublicKey"
} elseif ($UpdateURL -or $PublicKey) {
    throw "для автообновления нужны оба параметра: -UpdateURL и -PublicKey"
}
Build "fi-service.exe" "./cmd/fi-service" $updateFlags
Build "fi.exe" "./cmd/fi" "-H windowsgui" "production"
Build "fi-probe.exe" "./cmd/fi-probe"
Build "uninstall.exe" "./cmd/fi-setup" "-H windowsgui" "uninstaller"

Copy-Item build\fi-service.exe, build\fi.exe, build\uninstall.exe cmd\fi-setup\payload\ -Force
Build "fi-setup.exe" "./cmd/fi-setup" "-H windowsgui"

if ($SignKey) {
    if (-not $InstallerURL) { throw "для подписи укажите -InstallerURL — адрес, где будет лежать fi-setup.exe" }
    Write-Host "→ подпись обновления"
    Invoke-Go @("run", "./cmd/fi-sign", "sign", "-key", $SignKey, "-version", $Version, "-url", $InstallerURL, "-installer", "build\fi-setup.exe", "-out", "build\update.json")
}

Get-ChildItem build\*.exe | Select-Object Name, @{ n = "MB"; e = { [math]::Round($_.Length / 1MB, 1) } } | Format-Table -AutoSize
