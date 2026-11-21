# Устанавливает Janus для Windows в профиль пользователя.
# Права администратора не нужны.
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$target = Join-Path $env:LOCALAPPDATA "Janus"
New-Item -ItemType Directory -Force -Path $target | Out-Null
$exe = Join-Path $target "janus.exe"

Write-Host "== сборка =="
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "нужен Go 1.21 или новее: https://go.dev/dl/"
}
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-H=windowsgui -s -w" -o $exe .
if (-not (Test-Path $exe)) { Write-Error "сборка не удалась" }
Write-Host "собрано: $exe"

Write-Host "== автозапуск =="
& $exe install

Write-Host "== запуск =="
Get-Process janus -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 400
# Started detached so the console returns at once.
Start-Process -FilePath $exe -ArgumentList "daemon" -WindowStyle Hidden
Start-Sleep -Seconds 2

& $exe status
Write-Host ""
Write-Host "Готово. Настройки: http://127.0.0.1:8791/"
Write-Host "Диагностика:     $exe diag"
