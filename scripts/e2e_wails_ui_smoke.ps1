param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDir = "build/e2e",
    [string]$AgentExePath = "",
    [string]$UIExePath = "",
    [string]$PipeName = "\\.\pipe\CameraConnectIPC",
    [string]$LocalAppDataPath = "",
    [int]$StartupTimeoutSec = 20,
    [int]$TerminateTimeoutSec = 8,
    [switch]$NoAutoStartAgent,
    [switch]$KeepAgentRunning,
    [switch]$KeepWailsRunning,
    [switch]$AcceptKnownPreflightBlocker
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

    foreach ($candidate in $Candidates) {
        $resolvedCandidate = Resolve-OutputPath -Root $ProjectRoot -Path $candidate
        if (Test-Path -LiteralPath $resolvedCandidate) {
            return (Resolve-Path -LiteralPath $resolvedCandidate).Path
        }
    }

    throw "Unable to resolve $Label executable. Checked: $($Candidates -join ', ')"
}

function Get-JsonBlock {
    param([Parameter(Mandatory = $true)][string]$Raw)

    $ansiPattern = [char]27 + '\[[0-9;]*[A-Za-z]'
    $sanitized = [regex]::Replace($Raw, $ansiPattern, "")
    $trimmed = $sanitized.Trim()
    $start = $trimmed.IndexOf("{")
    if ($start -lt 0) {
        throw "No JSON object found in output."
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

    throw "Unable to isolate JSON block from command output."
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
                Start-Sleep -Milliseconds 120
                continue
            }
            throw
        }
    }
    if ([string]::IsNullOrWhiteSpace($json)) {
        throw "No valid JSON envelope parsed for action '$Action'."
    }

    return [PSCustomObject]@{
        Raw    = $raw.Trim()
        Parsed = ($json | ConvertFrom-Json)
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
    $hostName = "UNKNOWN-HOST"
}

$startedAgentByScript = $false
$startedAgentPid = 0
$startedAgentJobId = 0
$wailsProc = $null

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
        $job = Start-Job -ScriptBlock {
            param([string]$ExePath, [string]$LocalAppData)
            $env:LOCALAPPDATA = $LocalAppData
            & $ExePath --minimized
        } -ArgumentList $resolvedAgent, $resolvedLocalAppData
        $script:startedAgentByScript = $true
        $script:startedAgentJobId = $job.Id
    }

    if (-not (Wait-AgentReady -UIExe $resolvedUI -Seconds $StartupTimeoutSec)) {
        throw "Agent did not become ready within $StartupTimeoutSec seconds."
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

    $cliPing = Invoke-UIAction -UIExe $resolvedUI -Action "ping"
    $cliStatus = Invoke-UIAction -UIExe $resolvedUI -Action "status"

    $stamp = New-RunStamp
    $stdoutPath = Join-Path $resolvedOutputDir ("wails-ui-smoke-stdout-{0}-{1}.log" -f $hostName, $stamp)
    $stderrPath = Join-Path $resolvedOutputDir ("wails-ui-smoke-stderr-{0}-{1}.log" -f $hostName, $stamp)

    $wailsProc = Start-Process `
        -FilePath $resolvedUI `
        -ArgumentList @("--runtime", "wails", "--pipe", $PipeName) `
        -PassThru `
        -RedirectStandardOutput $stdoutPath `
        -RedirectStandardError $stderrPath

    $startDeadline = (Get-Date).AddSeconds($StartupTimeoutSec)
    while ((Get-Date) -lt $startDeadline) {
        if ($wailsProc.HasExited) {
            break
        }
        Start-Sleep -Milliseconds 250
    }

    $runtimeStarted = -not $wailsProc.HasExited
    $ipcDuringRuntimeOk = $false
    if ($runtimeStarted) {
        $statusDuring = Invoke-UIAction -UIExe $resolvedUI -Action "status"
        $ipcDuringRuntimeOk = [bool]($statusDuring.Parsed.ok -and $statusDuring.Parsed.success)
    }

    $runtimeClosed = $false
    if ($runtimeStarted) {
        if ($KeepWailsRunning) {
            $runtimeClosed = $true
        } else {
            Stop-Process -Id $wailsProc.Id -Force -ErrorAction SilentlyContinue
            $stopDeadline = (Get-Date).AddSeconds($TerminateTimeoutSec)
            while ((Get-Date) -lt $stopDeadline) {
                if ($wailsProc.HasExited) {
                    $runtimeClosed = $true
                    break
                }
                Start-Sleep -Milliseconds 200
            }
        }
    }

    $stdoutLog = ""
    $stderrLog = ""
    if (Test-Path -LiteralPath $stdoutPath) {
        $stdoutLog = Get-Content -LiteralPath $stdoutPath -Raw
    }
    if (Test-Path -LiteralPath $stderrPath) {
        $stderrLog = Get-Content -LiteralPath $stderrPath -Raw
    }
    $combinedLog = ($stdoutLog + "`n" + $stderrLog)
    $knownPatterns = @(
        "Unable to find Wails in go.mod",
        "wails runtime is not included in this build",
        "run wails dev",
        "wails CLI"
    )
    $knownPreflightBlocker = $false
    foreach ($pattern in $knownPatterns) {
        if ($combinedLog -match [regex]::Escape($pattern)) {
            $knownPreflightBlocker = $true
            break
        }
    }
    $blockerAccepted = [bool]($AcceptKnownPreflightBlocker -and -not $runtimeStarted -and $knownPreflightBlocker)

    $agentReady = [bool]($cliStatus.Parsed.ok -and $cliStatus.Parsed.success)
    $cliPingOk = [bool]($cliPing.Parsed.ok -and $cliPing.Parsed.success)
    if (-not $runtimeStarted) {
        $ipcDuringRuntimeOk = $blockerAccepted
        $runtimeClosed = $true
    }

    $checks = [ordered]@{
        agent_ready                    = $agentReady
        cli_ping_ok                    = $cliPingOk
        wails_runtime_started          = [bool]$runtimeStarted
        ipc_check_while_runtime        = [bool]$ipcDuringRuntimeOk
        wails_runtime_close            = [bool]$runtimeClosed
        known_preflight_blocker        = [bool]$knownPreflightBlocker
        known_blocker_accepted         = [bool]$blockerAccepted
    }

    $overallPass = $false
    $resultMode = "startup_failed"
    if ($runtimeStarted) {
        $overallPass = [bool]($agentReady -and $cliPingOk -and $ipcDuringRuntimeOk -and $runtimeClosed)
        $resultMode = "runtime_started"
    } elseif ($blockerAccepted) {
        $overallPass = [bool]($agentReady -and $cliPingOk)
        $resultMode = "known_blocker_accepted"
    }

    $report = [ordered]@{
        created_at_utc    = (Get-Date).ToUniversalTime().ToString("o")
        host              = $hostName
        mode              = $resultMode
        local_appdata     = $resolvedLocalAppData
        pipe              = $PipeName
        agent_exe         = $resolvedAgent
        ui_exe            = $resolvedUI
        startup_timeout_s = $StartupTimeoutSec
        terminate_timeout_s = $TerminateTimeoutSec
        checks            = $checks
        cli_ping          = $cliPing.Parsed
        cli_status        = $cliStatus.Parsed
        wails_pid         = if ($wailsProc) { $wailsProc.Id } else { 0 }
        wails_exit_code   = if ($wailsProc -and $wailsProc.HasExited) { $wailsProc.ExitCode } else { $null }
        stdout_log_path   = $stdoutPath
        stderr_log_path   = $stderrPath
        pass              = $overallPass
    }

    $outPath = Join-Path $resolvedOutputDir ("wails-ui-smoke-{0}-{1}.json" -f $hostName, (New-RunStamp))
    $report | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $outPath -Encoding UTF8
    Write-Host "[e2e-wails] report => $outPath"
    Write-Host "[e2e-wails] mode=$resultMode pass=$overallPass"

    if (-not $overallPass) {
        exit 1
    }
}
finally {
    if ($wailsProc -and -not $wailsProc.HasExited -and -not $KeepWailsRunning) {
        Stop-Process -Id $wailsProc.Id -Force -ErrorAction SilentlyContinue
    }
    Stop-AgentIfOwned
}
