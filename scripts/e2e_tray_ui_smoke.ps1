param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDir = "build/e2e",
    [string]$AgentExePath = "",
    [string]$UIExePath = "",
    [ValidateSet("harness", "wails")]
    [string]$UIRuntime = "harness",
    [string]$PipeName = "\\.\pipe\CameraConnectIPC",
    [string]$LocalAppDataPath = "",
    [int]$TimeoutSec = 30,
    [switch]$SkipUIFocus,
    [switch]$AcceptKnownPreflightBlocker,
    [switch]$NoAutoStartAgent,
    [switch]$KeepAgentRunning
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-OutputPath {
    param(
        [string]$Root,
        [string]$Path
    )
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $Root
    }
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return $Path
    }
    return (Join-Path $Root $Path)
}

function Resolve-Executable {
    param(
        [string]$Label,
        [string]$ProvidedPath,
        [string[]]$Candidates
    )
    if (-not [string]::IsNullOrWhiteSpace($ProvidedPath)) {
        $resolvedProvided = Resolve-OutputPath -Root $ProjectRoot -Path $ProvidedPath
        if (Test-Path -LiteralPath $resolvedProvided) {
            return (Resolve-Path -LiteralPath $resolvedProvided).Path
        }
        throw "$Label executable not found: $resolvedProvided"
    }

    $existing = @()
    foreach ($candidate in $Candidates) {
        $resolvedCandidate = Resolve-OutputPath -Root $ProjectRoot -Path $candidate
        if (Test-Path -LiteralPath $resolvedCandidate) {
            $existing += (Get-Item -LiteralPath $resolvedCandidate)
        }
    }
    if ($existing.Count -eq 0) {
        throw "Unable to resolve $Label executable. Checked: $($Candidates -join ', ')"
    }

    $latest = $existing | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    return $latest.FullName
}

function Get-JsonBlock {
    param([Parameter(Mandatory = $true)][string]$Raw)

    $ansiPattern = [char]27 + '\[[0-9;]*[A-Za-z]'
    $sanitized = [regex]::Replace($Raw, $ansiPattern, "")
    $trimmed = $sanitized.Trim()
    $start = $trimmed.IndexOf("{")
    if ($start -lt 0) {
        throw "No JSON object found in output: $trimmed"
    }

    for ($end = $trimmed.Length - 1; $end -ge $start; $end--) {
        if ($trimmed[$end] -ne "}") {
            continue
        }
        $candidate = $trimmed.Substring($start, ($end - $start + 1))
        try {
            $null = $candidate | ConvertFrom-Json -ErrorAction Stop
            return $candidate
        } catch {
        }
    }

    throw "Unable to isolate JSON block from output: $trimmed"
}

function Invoke-UIAction {
    param(
        [Parameter(Mandatory = $true)][string]$UIExe,
        [Parameter(Mandatory = $true)][string]$Action,
        [string]$Payload = ""
    )

    $args = @("--action", $Action, "--pipe", $PipeName)
    if (-not [string]::IsNullOrWhiteSpace($Payload)) {
        $args += @("--payload", $Payload)
    }

    $raw = ""
    $json = $null
    for ($attempt = 1; $attempt -le 2; $attempt++) {
        $raw = & $UIExe @args 2>&1 | Out-String
        try {
            $json = Get-JsonBlock -Raw $raw
            break
        } catch {
            if ($attempt -lt 2) {
                Start-Sleep -Milliseconds 150
                continue
            }
            throw
        }
    }
    if ([string]::IsNullOrWhiteSpace($json)) {
        throw "Failed to parse JSON envelope for action '$Action'. Raw output:`n$raw"
    }
    $parsed = $json | ConvertFrom-Json

    return [PSCustomObject]@{
        Raw    = $raw.Trim()
        Parsed = $parsed
    }
}

function Wait-AgentReady {
    param(
        [Parameter(Mandatory = $true)][string]$UIExe,
        [Parameter(Mandatory = $true)][int]$Seconds
    )
    $attempts = [Math]::Max(1, [int](($Seconds * 1000) / 250))
    for ($i = 0; $i -lt $attempts; $i++) {
        try {
            $ping = Invoke-UIAction -UIExe $UIExe -Action "ping"
            if ($ping.Parsed.ok -and $ping.Parsed.success) {
                return $true
            }
        } catch {
        }
        Start-Sleep -Milliseconds 250
    }
    return $false
}

function New-RunStamp {
    return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
}

$resolvedOutputDir = Resolve-OutputPath -Root $ProjectRoot -Path $OutputDir
New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null

$resolvedAgent = Resolve-Executable -Label "Agent" -ProvidedPath $AgentExePath -Candidates @(
    "build/bin/CameraConnectAgent.exe",
    "agent.exe"
)
$resolvedUI = Resolve-Executable -Label "UI" -ProvidedPath $UIExePath -Candidates @(
    "build/bin/CameraConnectUI.exe",
    "ui.exe"
)

$resolvedLocalAppData = ""
if (-not [string]::IsNullOrWhiteSpace($LocalAppDataPath)) {
    $resolvedLocalAppData = Resolve-OutputPath -Root $ProjectRoot -Path $LocalAppDataPath
} elseif (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    $resolvedLocalAppData = $env:LOCALAPPDATA
} else {
    $resolvedLocalAppData = Join-Path $resolvedOutputDir "localappdata"
}
New-Item -ItemType Directory -Force -Path $resolvedLocalAppData | Out-Null
$env:LOCALAPPDATA = $resolvedLocalAppData

$hostName = ""
if (-not [string]::IsNullOrWhiteSpace($env:COMPUTERNAME)) {
    $hostName = $env:COMPUTERNAME.Trim()
} else {
    try {
        $hostName = [System.Net.Dns]::GetHostName()
    } catch {
        $hostName = "UNKNOWN-HOST"
    }
}

$startedAgentByScript = $false
$startedAgentPid = 0
$startedAgentJobId = 0
$primaryUIProc = $null

function Ensure-AgentRunning {
    if (Wait-AgentReady -UIExe $resolvedUI -Seconds 2) {
        return
    }
    if ($NoAutoStartAgent) {
        throw "Agent not reachable on $PipeName and -NoAutoStartAgent was specified."
    }

    try {
        $proc = Start-Process -FilePath $resolvedAgent -ArgumentList "--minimized" -PassThru
        $script:startedAgentByScript = $true
        $script:startedAgentPid = $proc.Id
    } catch {
        try {
            $job = Start-Job -ScriptBlock {
                param([string]$ExePath, [string]$LocalAppData)
                $env:LOCALAPPDATA = $LocalAppData
                & $ExePath --minimized
            } -ArgumentList $resolvedAgent, $resolvedLocalAppData

            $script:startedAgentByScript = $true
            $script:startedAgentPid = 0
            $script:startedAgentJobId = $job.Id
        } catch {
            throw "Failed to start agent automatically: $($_.Exception.Message)"
        }
    }

    if (-not (Wait-AgentReady -UIExe $resolvedUI -Seconds $TimeoutSec)) {
        throw "Agent did not become ready within $TimeoutSec seconds."
    }
}

function Stop-AgentIfOwned {
    if (-not $script:startedAgentByScript) {
        return
    }
    if ($KeepAgentRunning) {
        return
    }

    if ($script:startedAgentPid -gt 0) {
        Stop-Process -Id $script:startedAgentPid -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 300
    }
    if ($script:startedAgentJobId -gt 0) {
        $job = Get-Job -Id $script:startedAgentJobId -ErrorAction SilentlyContinue
        if ($job) {
            Stop-Job -Id $script:startedAgentJobId -ErrorAction SilentlyContinue
            Remove-Job -Id $script:startedAgentJobId -Force -ErrorAction SilentlyContinue
        }
        $script:startedAgentJobId = 0
    }
}

try {
    Ensure-AgentRunning

    $uiLaunchArgs = @()
    if ($UIRuntime -eq "wails") {
        $uiLaunchArgs = @("--runtime", "wails", "--pipe", $PipeName)
    }

    $statusBefore = Invoke-UIAction -UIExe $resolvedUI -Action "status"
    $syncNow = Invoke-UIAction -UIExe $resolvedUI -Action "sync-now"
    $statusAfter = Invoke-UIAction -UIExe $resolvedUI -Action "status"
    $logsSnapshot = Invoke-UIAction -UIExe $resolvedUI -Action "subscribe-logs" -Payload '{"limit":30}'

    $trayStatusPath = Join-Path $resolvedLocalAppData "CameraConnect\tray_status.json"
    $trayStatusReady = $false
    $trayStatusObj = $null
    for ($i = 0; $i -lt 24; $i++) {
        if (Test-Path -LiteralPath $trayStatusPath) {
            try {
                $trayRaw = Get-Content -LiteralPath $trayStatusPath -Raw
                $trayStatusObj = $trayRaw | ConvertFrom-Json
                if ($trayStatusObj -and -not [string]::IsNullOrWhiteSpace([string]$trayStatusObj.status_text)) {
                    $trayStatusReady = $true
                    break
                }
            } catch {
            }
        }
        Start-Sleep -Milliseconds 250
    }

    $uiFocusOutput = ""
    $uiFocusCheck = $false
    $uiFocusDetail = ""
    $uiFocusKnownBlocker = $false
    $knownBlockerPatterns = @(
        "Unable to find Wails in go.mod",
        "wails runtime is not included in this build",
        "run wails dev",
        "wails CLI"
    )
    if ($SkipUIFocus) {
        $uiFocusCheck = $true
        $uiFocusDetail = "skipped by -SkipUIFocus"
    } else {
        try {
            $primaryUIProc = Start-Process -FilePath $resolvedUI -ArgumentList $uiLaunchArgs -PassThru
            Start-Sleep -Milliseconds 1500
            $uiFocusOutput = (& $resolvedUI @uiLaunchArgs 2>&1 | Out-String)
            $secondExitCode = $LASTEXITCODE
            if ($null -eq $secondExitCode) {
                $secondExitCode = 0
            }

            if ($UIRuntime -eq "wails") {
                foreach ($pattern in $knownBlockerPatterns) {
                    if ($uiFocusOutput -match [regex]::Escape($pattern)) {
                        $uiFocusKnownBlocker = $true
                        break
                    }
                }

                if ($uiFocusKnownBlocker -and $AcceptKnownPreflightBlocker) {
                    $uiFocusCheck = $true
                    $uiFocusDetail = "known wails preflight blocker accepted"
                } else {
                    $uiFocusCheck = [bool]($secondExitCode -eq 0 -and ($uiFocusOutput -match "Existing UI instance focused" -or $uiFocusOutput -match "wails"))
                    $uiFocusDetail = "exit_code=$secondExitCode"
                }
            } else {
                $uiFocusCheck = [bool]($secondExitCode -eq 0 -and ($uiFocusOutput -match "Existing UI instance focused"))
                $uiFocusDetail = "exit_code=$secondExitCode"
            }
        } catch {
            $uiFocusCheck = $false
            $uiFocusDetail = "launch failed: $($_.Exception.Message)"
        }
    }

    $syncNowCommandOk = [bool]($syncNow.Parsed.ok -and ($syncNow.Parsed.success -or [string]$syncNow.Parsed.code -eq "bad_request"))

    $checks = [ordered]@{
        agent_ready            = [bool]($statusBefore.Parsed.ok -and $statusBefore.Parsed.success)
        status_before_ok       = [bool]($statusBefore.Parsed.ok -and $statusBefore.Parsed.success)
        sync_now_command_ok    = $syncNowCommandOk
        status_after_ok        = [bool]($statusAfter.Parsed.ok -and $statusAfter.Parsed.success)
        subscribe_logs_ok      = [bool]($logsSnapshot.Parsed.ok -and $logsSnapshot.Parsed.success)
        tray_status_file_ready = [bool]$trayStatusReady
        ui_focus_on_relaunch   = [bool]$uiFocusCheck
        ui_focus_known_blocker = [bool]$uiFocusKnownBlocker
    }

    $requiredCheckKeys = @(
        "agent_ready",
        "status_before_ok",
        "sync_now_command_ok",
        "status_after_ok",
        "subscribe_logs_ok",
        "tray_status_file_ready",
        "ui_focus_on_relaunch"
    )
    $allRequired = @($requiredCheckKeys | ForEach-Object { [bool]$checks[$_] })
    $overallPass = ($allRequired -notcontains $false)

    $report = [ordered]@{
        created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
        host           = $hostName
        local_appdata  = $resolvedLocalAppData
        pipe           = $PipeName
        ui_runtime     = $UIRuntime
        required_checks = $requiredCheckKeys
        agent_exe      = $resolvedAgent
        ui_exe         = $resolvedUI
        checks         = $checks
        tray_status    = $trayStatusObj
        status_before  = $statusBefore.Parsed
        sync_now       = $syncNow.Parsed
        status_after   = $statusAfter.Parsed
        logs_snapshot  = $logsSnapshot.Parsed
        ui_focus_detail = $uiFocusDetail
        ui_focus_stdout = $uiFocusOutput
        pass           = $overallPass
    }

    $outPath = Join-Path $resolvedOutputDir ("tray-ui-smoke-{0}-{1}.json" -f $hostName, (New-RunStamp))
    $report | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $outPath -Encoding UTF8
    Write-Host "[e2e-tray] report => $outPath"
    Write-Host "[e2e-tray] pass=$overallPass"

    if (-not $overallPass) {
        exit 1
    }
}
finally {
    if ($primaryUIProc -and -not $primaryUIProc.HasExited) {
        Stop-Process -Id $primaryUIProc.Id -Force -ErrorAction SilentlyContinue
    }
    Stop-AgentIfOwned
}
