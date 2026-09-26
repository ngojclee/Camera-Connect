param(
    [string]$AgentExePath = "",
    [string]$UIExePath = "",
    [int]$TimeoutSec = 45,
    [switch]$SkipUIFocus = $false,
    [switch]$KeepProcesses = $false
)

$ErrorActionPreference = "Stop"

function Resolve-Executable {
    param(
        [string]$Label,
        [string]$Provided,
        [string[]]$Candidates
    )

    if (-not [string]::IsNullOrWhiteSpace($Provided)) {
        if (Test-Path -LiteralPath $Provided) {
            return (Resolve-Path -LiteralPath $Provided).Path
        }
        throw "$Label path not found: $Provided"
    }

    $existing = @()
    foreach ($candidate in $Candidates) {
        if (Test-Path -LiteralPath $candidate) {
            $existing += (Get-Item -LiteralPath $candidate)
        }
    }
    if ($existing.Count -gt 0) {
        $latest = $existing | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        return $latest.FullName
    }

    throw "Unable to resolve $Label executable. Checked: $($Candidates -join ', ')"
}

function Invoke-UIAction {
    param(
        [string]$UIExe,
        [string]$Action,
        [string]$Payload = ""
    )

    if ([string]::IsNullOrWhiteSpace($Payload)) {
        $raw = & $UIExe --action $Action 2>&1 | Out-String
    } else {
        $raw = & $UIExe --action $Action --payload $Payload 2>&1 | Out-String
    }

    $obj = $null
    $rawTrim = $raw.Trim()
    try {
        $obj = $rawTrim | ConvertFrom-Json
    } catch {
        $jsonStart = $rawTrim.IndexOf("{")
        if ($jsonStart -ge 0) {
            $jsonCandidate = $rawTrim.Substring($jsonStart)
            try {
                $obj = $jsonCandidate | ConvertFrom-Json
            } catch {
            }
        }
    }

    return [PSCustomObject]@{
        Raw = $rawTrim
        Obj = $obj
    }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

$resolvedAgent = Resolve-Executable -Label "Agent" -Provided $AgentExePath -Candidates @(
    (Join-Path $repoRoot "build\bin\CameraConnectAgent.exe"),
    (Join-Path $repoRoot "agent.exe")
)
$resolvedUI = Resolve-Executable -Label "UI" -Provided $UIExePath -Candidates @(
    (Join-Path $repoRoot "build\bin\CameraConnectUI.exe"),
    (Join-Path $repoRoot "ui.exe")
)

$runDir = Join-Path $repoRoot ("temp_scripts\phase0_2_run_" + (Get-Date -Format "yyyyMMdd_HHmmss"))
New-Item -ItemType Directory -Path $runDir -Force | Out-Null

if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    $env:LOCALAPPDATA = Join-Path $runDir "localappdata_fallback"
    New-Item -ItemType Directory -Path $env:LOCALAPPDATA -Force | Out-Null
}

$agentLog = Join-Path $runDir "agent.log"
$agentErrLog = Join-Path $runDir "agent.err.log"
$uiFocusLog = Join-Path $runDir "ui_focus_second_launch.log"
$uiFocusErrLog = Join-Path $runDir "ui_focus_second_launch.err.log"

$summary = [ordered]@{
    run_dir = $runDir
    agent_exe = $resolvedAgent
    ui_exe = $resolvedUI
    steps = [ordered]@{
        tray_bootstrap = [ordered]@{
            ok = $false
            detail = ""
        }
        ipc_roundtrip = [ordered]@{
            ok = $false
            detail = ""
        }
        ui_launch_focus = [ordered]@{
            ok = $false
            skipped = [bool]$SkipUIFocus
            detail = ""
        }
    }
}

$agentProc = $null
$uiProc = $null

try {
    $agentProc = Start-Process -FilePath $resolvedAgent -ArgumentList "--minimized" -PassThru -RedirectStandardOutput $agentLog -RedirectStandardError $agentErrLog

    $deadline = (Get-Date).AddSeconds([Math]::Max(5, $TimeoutSec))

    $pingOk = $false
    $lastPingRaw = ""
    while ((Get-Date) -lt $deadline) {
        try {
            $ping = Invoke-UIAction -UIExe $resolvedUI -Action "ping"
            $lastPingRaw = $ping.Raw
            if ($ping.Obj -and $ping.Obj.ok -eq $true -and $ping.Obj.success -eq $true) {
                $pingOk = $true
                break
            }
        } catch {
        }
        Start-Sleep -Milliseconds 250
    }

    $trayStatusPath = Join-Path $env:LOCALAPPDATA "CameraConnect\tray_status.json"
    $trayFileOk = $false
    $trayDeadline = (Get-Date).AddSeconds(8)
    while ((Get-Date) -lt $trayDeadline) {
        if (Test-Path -LiteralPath $trayStatusPath) {
            try {
                $trayRaw = Get-Content -LiteralPath $trayStatusPath -Raw
                $trayContent = $trayRaw | ConvertFrom-Json
                if ($trayContent -and $trayContent.status_text) {
                    $trayFileOk = $true
                    break
                }
            } catch {
            }
        }
        Start-Sleep -Milliseconds 250
    }

    $trayLog = ""
    if (Test-Path -LiteralPath $agentLog) {
        $trayLog = Get-Content -LiteralPath $agentLog -Raw
    }
    if (Test-Path -LiteralPath $agentErrLog) {
        $trayLog += "`n" + (Get-Content -LiteralPath $agentErrLog -Raw)
    }
    $trayLogOk = $trayLog -match "Tray bootstrap started"

    $summary.steps.tray_bootstrap.ok = ($trayFileOk -and $trayLogOk)
    $summary.steps.tray_bootstrap.detail = "status_file_ok=$trayFileOk; agent_log_ok=$trayLogOk; status_file=$trayStatusPath"

    $agentErr = ""
    if (Test-Path -LiteralPath $agentErrLog) {
        $agentErr = (Get-Content -LiteralPath $agentErrLog -Raw).Trim()
    }

    if ($pingOk) {
        $statusResult = Invoke-UIAction -UIExe $resolvedUI -Action "status"
        if ($statusResult.Obj -and $statusResult.Obj.ok -eq $true -and $statusResult.Obj.success -eq $true) {
            $summary.steps.ipc_roundtrip.ok = $true
            $summary.steps.ipc_roundtrip.detail = "status command succeeded"
        } else {
            $summary.steps.ipc_roundtrip.ok = $false
            $summary.steps.ipc_roundtrip.detail = "status command failed: $($statusResult.Raw)"
        }
    } else {
        $summary.steps.ipc_roundtrip.ok = $false
        $summary.steps.ipc_roundtrip.detail = "ping not ready before timeout; last_ping_raw=$lastPingRaw; agent_err=$agentErr"
    }

    if (-not $SkipUIFocus -and $pingOk) {
        $uiProc = Start-Process -FilePath $resolvedUI -PassThru
        Start-Sleep -Seconds 2

        $secondProc = Start-Process -FilePath $resolvedUI -PassThru -RedirectStandardOutput $uiFocusLog -RedirectStandardError $uiFocusErrLog
        if (-not $secondProc.WaitForExit(8000)) {
            Stop-Process -Id $secondProc.Id -Force -ErrorAction SilentlyContinue
            $summary.steps.ui_launch_focus.ok = $false
            $summary.steps.ui_launch_focus.detail = "second UI launch did not exit in expected time"
        } else {
            $focusOut = ""
            if (Test-Path -LiteralPath $uiFocusLog) {
                $focusOut = Get-Content -LiteralPath $uiFocusLog -Raw
            }
            $focusOk = ($secondProc.ExitCode -eq 0 -and $focusOut -match "Existing UI instance focused")
            $summary.steps.ui_launch_focus.ok = $focusOk
            $summary.steps.ui_launch_focus.detail = "exit_code=$($secondProc.ExitCode); matched_focus_log=$($focusOut -match 'Existing UI instance focused')"
        }
    } elseif ($SkipUIFocus) {
        $summary.steps.ui_launch_focus.ok = $true
        $summary.steps.ui_launch_focus.detail = "skipped by -SkipUIFocus"
    } else {
        $summary.steps.ui_launch_focus.ok = $false
        $summary.steps.ui_launch_focus.detail = "skipped because ping did not become ready"
    }
}
finally {
    if (-not $KeepProcesses) {
        if ($uiProc -and -not $uiProc.HasExited) {
            Stop-Process -Id $uiProc.Id -Force -ErrorAction SilentlyContinue
        }
        if ($agentProc -and -not $agentProc.HasExited) {
            Stop-Process -Id $agentProc.Id -Force -ErrorAction SilentlyContinue
        }
    }
}

$allOk = ($summary.steps.tray_bootstrap.ok -and $summary.steps.ipc_roundtrip.ok -and $summary.steps.ui_launch_focus.ok)
$summary["overall_ok"] = $allOk
$summary["checked_at"] = (Get-Date).ToString("o")

$summary | ConvertTo-Json -Depth 8

if (-not $allOk) {
    exit 1
}
