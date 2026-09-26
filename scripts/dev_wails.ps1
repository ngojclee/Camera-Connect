$ErrorActionPreference = "Stop"

$UI_DIR = Join-Path $PSScriptRoot "..\cmd\ui"

Write-Host "Starting Wails development server..." -ForegroundColor Cyan
Push-Location $UI_DIR
try {
    wails dev -tags wails
} finally {
    Pop-Location
}
