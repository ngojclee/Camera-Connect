param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$InstallerPath = "",
    [string]$OutputDir = "build/e2e",
    [string]$InstallDir = "",
    [switch]$SkipUninstall,
    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if (-not $InstallDir) {
    if ($env:LOCALAPPDATA) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\Camera Connect"
    } elseif ($DryRun) {
        $InstallDir = Join-Path $ProjectRoot "build\dryrun-install\Camera Connect"
    } else {
        throw "LOCALAPPDATA is not set; please provide -InstallDir explicitly."
    }
}

$resolvedOutputDir = Join-Path $ProjectRoot $OutputDir
New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null

function Resolve-InstallerPath {
    param(
        [string]$ProjectRoot,
        [string]$InstallerPath,
        [bool]$DryRun
    )

    if (-not [string]::IsNullOrWhiteSpace($InstallerPath)) {
        if ($DryRun) {
            return $InstallerPath
        }
        if (-not (Test-Path -LiteralPath $InstallerPath)) {
            throw "Installer not found: $InstallerPath"
        }
        return (Resolve-Path -LiteralPath $InstallerPath).Path
    }

    $searchRoot = Join-Path $ProjectRoot "build\installer"
    $candidate = Get-ChildItem -Path $searchRoot -Filter "CameraConnectSetup-v*-windows-amd64.exe" -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1

    if ($candidate) {
        return $candidate.FullName
    }

    if ($DryRun) {
        return Join-Path $searchRoot "CameraConnectSetup-v<version>-windows-amd64.exe"
    }
    throw "No installer found under $searchRoot"
}

function Get-StartupRegistryValue {
    $runPath = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    try {
        $item = Get-ItemProperty -Path $runPath -Name "CameraConnect" -ErrorAction Stop
        return [string]$item.CameraConnect
    } catch {
        return ""
    }
}

function Stop-CameraConnectProcesses {
    $names = @("CameraConnectAgent", "CameraConnectUI")
    foreach ($name in $names) {
        Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
            try {
                Stop-Process -Id $_.Id -Force -ErrorAction Stop
            } catch {
            }
        }
    }
}

function Start-CameraProcess {
    param(
        [string]$ExePath,
        [string]$Arguments = ""
    )
    if (-not (Test-Path -LiteralPath $ExePath)) {
        return $null
    }
    try {
        return Start-Process -FilePath $ExePath -ArgumentList $Arguments -PassThru -ErrorAction Stop
    } catch {
        return $null
    }
}

function Invoke-Executable {
    param(
        [string]$FilePath,
        [string]$Arguments,
        [string]$Phase,
        [bool]$DryRun
    )

    if ($DryRun) {
        return [ordered]@{
            phase       = $Phase
            skipped     = $true
            command     = $FilePath
            arguments   = $Arguments
            exit_code   = 0
            success     = $true
            started_utc = (Get-Date).ToUniversalTime().ToString("o")
            ended_utc   = (Get-Date).ToUniversalTime().ToString("o")
        }
    }

    $started = (Get-Date).ToUniversalTime()
    $proc = Start-Process -FilePath $FilePath -ArgumentList $Arguments -Wait -PassThru -ErrorAction Stop
    $ended = (Get-Date).ToUniversalTime()

    return [ordered]@{
        phase       = $Phase
        skipped     = $false
        command     = $FilePath
        arguments   = $Arguments
        exit_code   = [int]$proc.ExitCode
        success     = ($proc.ExitCode -eq 0)
        started_utc = $started.ToString("o")
        ended_utc   = $ended.ToString("o")
    }
}

function Find-UninstallEntry {
    $roots = @(
        "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall"
    )
    foreach ($root in $roots) {
        $items = Get-ChildItem -Path $root -ErrorAction SilentlyContinue
        foreach ($item in $items) {
            $props = Get-ItemProperty -LiteralPath $item.PSPath -ErrorAction SilentlyContinue
            if (-not $props) { continue }
            $displayNameProp = $props.PSObject.Properties["DisplayName"]
            $uninstallProp = $props.PSObject.Properties["UninstallString"]
            $quietUninstallProp = $props.PSObject.Properties["QuietUninstallString"]
            $displayVersionProp = $props.PSObject.Properties["DisplayVersion"]

            $displayName = if ($displayNameProp) { [string]$displayNameProp.Value } else { "" }
            if ($displayName -ne "Camera Connect") { continue }

            $uninstallString = if ($uninstallProp) { [string]$uninstallProp.Value } else { "" }
            if (-not [string]::IsNullOrWhiteSpace($uninstallString)) {
                return [ordered]@{
                    key_path          = $item.PSPath
                    uninstall_string  = $uninstallString
                    quiet_uninstall   = if ($quietUninstallProp) { [string]$quietUninstallProp.Value } else { "" }
                    display_version   = if ($displayVersionProp) { [string]$displayVersionProp.Value } else { "" }
                }
            }
        }
    }
    return $null
}

function Split-UninstallCommand {
    param([string]$UninstallString)

    $text = [string]$UninstallString
    if ([string]::IsNullOrWhiteSpace($text)) {
        return $null
    }

    $trimmed = $text.Trim()
    if ($trimmed.StartsWith('"')) {
        $closing = $trimmed.IndexOf('"', 1)
        if ($closing -gt 1) {
            $exe = $trimmed.Substring(1, $closing - 1)
            $args = $trimmed.Substring($closing + 1).Trim()
            return [ordered]@{ exe = $exe; args = $args }
        }
    }

    $firstSpace = $trimmed.IndexOf(" ")
    if ($firstSpace -gt 0) {
        return [ordered]@{
            exe  = $trimmed.Substring(0, $firstSpace)
            args = $trimmed.Substring($firstSpace + 1).Trim()
        }
    }

    return [ordered]@{ exe = $trimmed; args = "" }
}

$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$installLogPath = Join-Path $resolvedOutputDir ("installer-install-{0}.log" -f $timestamp)
$upgradeLogPath = Join-Path $resolvedOutputDir ("installer-upgrade-{0}.log" -f $timestamp)
$uninstallLogPath = Join-Path $resolvedOutputDir ("installer-uninstall-{0}.log" -f $timestamp)
$reportPath = Join-Path $resolvedOutputDir ("installer-regression-{0}.json" -f $timestamp)

$report = [ordered]@{
    created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    dry_run        = [bool]$DryRun
    installer_path = ""
    install_dir    = $InstallDir
    phases         = @()
    checks         = [ordered]@{}
    warnings       = @()
    errors         = @()
}

try {
    $resolvedInstallerPath = Resolve-InstallerPath -ProjectRoot $ProjectRoot -InstallerPath $InstallerPath -DryRun:$DryRun
    $report.installer_path = $resolvedInstallerPath

    Stop-CameraConnectProcesses

    $installArgs = "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /TASKS=`"startupagent`" /LOG=`"$installLogPath`""
    $installPhase = Invoke-Executable -FilePath $resolvedInstallerPath -Arguments $installArgs -Phase "install" -DryRun:$DryRun
    $report.phases += $installPhase
    if (-not $installPhase.success) {
        $report.errors += "Install phase failed with exit code $($installPhase.exit_code)."
    }

    $agentPath = Join-Path $InstallDir "CameraConnectAgent.exe"
    $uiPath = Join-Path $InstallDir "CameraConnectUI.exe"
    $report.checks.agent_binary_exists_after_install = (Test-Path -LiteralPath $agentPath)
    $report.checks.ui_binary_exists_after_install = (Test-Path -LiteralPath $uiPath)

    $startupValue = Get-StartupRegistryValue
    $report.checks.startup_registry_set_after_install = (-not [string]::IsNullOrWhiteSpace($startupValue))
    $report.checks.startup_registry_value_after_install = $startupValue

    if (-not $DryRun) {
        $startedAgent = Start-CameraProcess -ExePath $agentPath -Arguments "--minimized"
        $startedUI = Start-CameraProcess -ExePath $uiPath
        Start-Sleep -Milliseconds 600

        $report.checks.agent_process_started = [bool]$startedAgent
        $report.checks.ui_process_started = [bool]$startedUI
    } else {
        $report.checks.agent_process_started = $true
        $report.checks.ui_process_started = $true
    }

    $upgradeArgs = "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /TASKS=`"startupagent`" /LOG=`"$upgradeLogPath`""
    $upgradePhase = Invoke-Executable -FilePath $resolvedInstallerPath -Arguments $upgradeArgs -Phase "upgrade_reinstall" -DryRun:$DryRun
    $report.phases += $upgradePhase
    if (-not $upgradePhase.success) {
        $report.errors += "Upgrade phase failed with exit code $($upgradePhase.exit_code)."
    }

    if (-not $SkipUninstall) {
        $uninstallEntry = Find-UninstallEntry
        if (-not $uninstallEntry -and -not $DryRun) {
            $report.errors += "Could not locate uninstall entry for Camera Connect."
        } else {
            $command = if ($uninstallEntry -and $uninstallEntry.quiet_uninstall) { $uninstallEntry.quiet_uninstall } elseif ($uninstallEntry) { $uninstallEntry.uninstall_string } else { "<uninstaller-path>" }
            $parts = Split-UninstallCommand -UninstallString $command
            if (-not $parts -and -not $DryRun) {
                $report.errors += "Unable to parse uninstall command."
            } else {
                $uninstallExe = if ($parts) { [string]$parts.exe } else { "<uninstaller-path>" }
                $uninstallArgs = if ($parts) { [string]$parts.args } else { "" }
                if ([string]::IsNullOrWhiteSpace($uninstallArgs)) {
                    $uninstallArgs = "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
                } else {
                    $uninstallArgs += " /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
                }
                $uninstallArgs += " /LOG=`"$uninstallLogPath`""

                $uninstallPhase = Invoke-Executable -FilePath $uninstallExe -Arguments $uninstallArgs -Phase "uninstall" -DryRun:$DryRun
                $report.phases += $uninstallPhase
                if (-not $uninstallPhase.success) {
                    $report.errors += "Uninstall phase failed with exit code $($uninstallPhase.exit_code)."
                }
            }
        }
    } else {
        $report.warnings += "SkipUninstall specified: uninstall validation skipped."
    }

    if (-not $DryRun -and -not $SkipUninstall) {
        Start-Sleep -Milliseconds 500
        Stop-CameraConnectProcesses
    }

    $startupAfter = Get-StartupRegistryValue
    $report.checks.startup_registry_cleared_after_uninstall = ($SkipUninstall -or $DryRun -or [string]::IsNullOrWhiteSpace($startupAfter))
    $report.checks.startup_registry_value_after_uninstall = $startupAfter

    if (-not $DryRun -and -not $SkipUninstall) {
        $report.checks.install_dir_removed_after_uninstall = (-not (Test-Path -LiteralPath $InstallDir))
    } else {
        $report.checks.install_dir_removed_after_uninstall = $true
    }

    $runningAgent = @(Get-Process -Name "CameraConnectAgent" -ErrorAction SilentlyContinue)
    $runningUI = @(Get-Process -Name "CameraConnectUI" -ErrorAction SilentlyContinue)
    $report.checks.agent_process_count = $runningAgent.Count
    $report.checks.ui_process_count = $runningUI.Count
    $report.checks.no_orphan_processes = ($runningAgent.Count -eq 0 -and $runningUI.Count -eq 0)

    if (-not $report.checks.no_orphan_processes) {
        $report.warnings += "Detected running CameraConnect process(es) after regression script."
    }
} catch {
    $report.errors += $_.Exception.Message
}

$report.success = ($report.errors.Count -eq 0)
$report | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $reportPath -Encoding UTF8

if ($report.success) {
    Write-Host "[e2e-installer] PASS => $reportPath"
    exit 0
}

Write-Host "[e2e-installer] FAIL => $reportPath"
foreach ($err in $report.errors) {
    Write-Host "  - $err"
}
exit 1
