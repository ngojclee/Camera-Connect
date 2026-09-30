param(
    [string]$Version = "3.0.0.7",
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$SourceBinDir = "build/bin",
    [string]$OutputDir = "build/installer",
    [string]$InstallerScript = "installer/CameraConnectSetup.iss",
    [ValidateSet("wails")]
    [string]$UIRuntime = "wails",
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($Version -notmatch '^\d+\.\d+\.\d+\.\d+$') {
    throw "Version must match x.y.z.k format, received: $Version"
}

if (-not (Test-Path -LiteralPath $ProjectRoot)) {
    throw "ProjectRoot not found: $ProjectRoot"
}

$UIRuntime = $UIRuntime.Trim().ToLowerInvariant()

function Resolve-IsccPath {
    if ($env:ISCC_PATH -and (Test-Path -LiteralPath $env:ISCC_PATH)) {
        return (Resolve-Path -LiteralPath $env:ISCC_PATH).Path
    }

    $fromCommand = Get-Command iscc.exe -ErrorAction SilentlyContinue
    if ($fromCommand) {
        return $fromCommand.Source
    }

    $candidates = @(
        "C:\Program Files\Inno Setup 6\ISCC.exe",
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
    )
    if ($env:ProgramFiles -and (Test-Path -LiteralPath $env:ProgramFiles)) {
        $candidates += (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
    }
    $programFilesX86 = ${env:ProgramFiles(x86)}
    if ($programFilesX86 -and (Test-Path -LiteralPath $programFilesX86)) {
        $candidates += (Join-Path $programFilesX86 "Inno Setup 6\ISCC.exe")
    }
    $candidates = @($candidates | Where-Object { Test-Path -LiteralPath $_ })

    if ($candidates.Count -gt 0) {
        return (Resolve-Path -LiteralPath $candidates[0]).Path
    }

    throw "ISCC.exe not found. Install Inno Setup 6 or set ISCC_PATH."
}

$resolvedSourceBinDir = Join-Path $ProjectRoot $SourceBinDir
$resolvedOutputDir = Join-Path $ProjectRoot $OutputDir
$resolvedInstallerScript = Join-Path $ProjectRoot $InstallerScript

if (-not (Test-Path -LiteralPath $resolvedInstallerScript)) {
    throw "Installer script not found: $resolvedInstallerScript"
}

if (-not $SkipBuild) {
    Write-Host "[installer] Building binaries first..."
    & (Join-Path $ProjectRoot "scripts/build_windows.ps1") `
        -Version $Version `
        -OutputDir $SourceBinDir `
        -UIRuntime $UIRuntime `
        -SkipReleaseAssets
    if ($LASTEXITCODE -ne 0) {
        throw "build_windows.ps1 failed"
    }
}

$uiBinaryName = @("CameraConnect.exe", "CameraConnectUI.exe") |
    Where-Object { Test-Path -LiteralPath (Join-Path $resolvedSourceBinDir $_) } |
    Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($uiBinaryName)) {
    throw "Required UI binary missing in $resolvedSourceBinDir. Expected one of: CameraConnect.exe, CameraConnectUI.exe"
}

$requiredBinaries = @(
    (Join-Path $resolvedSourceBinDir "CameraConnectAgent.exe"),
    (Join-Path $resolvedSourceBinDir $uiBinaryName)
)

foreach ($binary in $requiredBinaries) {
    if (-not (Test-Path -LiteralPath $binary)) {
        throw "Required binary missing: $binary"
    }
}

New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null
$iscc = Resolve-IsccPath

Write-Host "[installer] ISCC=$iscc"
Write-Host "[installer] Version=$Version"
Write-Host "[installer] SourceBinDir=$resolvedSourceBinDir"
Write-Host "[installer] OutputDir=$resolvedOutputDir"
Write-Host "[installer] UIRuntime=$UIRuntime"
Write-Host "[installer] UIBinaryName=$uiBinaryName"

Push-Location $ProjectRoot
try {
    & $iscc `
        "/DAppVersion=$Version" `
        "/DSourceBinDir=$resolvedSourceBinDir" `
        "/DOutputDir=$resolvedOutputDir" `
        "/DUIRuntime=$UIRuntime" `
        "/DUIRuntimeRequested=$UIRuntime" `
        "/DUIBinaryName=$uiBinaryName" `
        $resolvedInstallerScript

    if ($LASTEXITCODE -ne 0) {
        throw "ISCC compile failed"
    }

    $installerPath = Join-Path $resolvedOutputDir "CameraConnectSetup-v$Version-windows-amd64.exe"
    if (-not (Test-Path -LiteralPath $installerPath)) {
        throw "Installer output missing: $installerPath"
    }

    Write-Host "[installer] OK"
    Write-Host "  Setup : $installerPath"
} finally {
    Pop-Location
}
