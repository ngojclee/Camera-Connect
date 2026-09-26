param(
    [ValidateSet("latency", "snapshot", "all")]
    [string]$Mode = "all",
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$BinDir = "build/bin",
    [string]$OutputDir = "build/e2e",
    [int]$Iterations = 80,
    [int]$IntervalMs = 40,
    [double]$P95TargetMs = 100,
    [string]$PipeName = "\\.\pipe\CameraConnectIPC",
    [string]$LocalAppDataPath = "",
    [switch]$NoAutoStartAgent,
    [switch]$KeepAgentRunning
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$resolvedBinDir = Join-Path $ProjectRoot $BinDir
$resolvedOutputDir = Join-Path $ProjectRoot $OutputDir
$uiExe = Join-Path $resolvedBinDir "CameraConnectUI.exe"
$agentExe = Join-Path $resolvedBinDir "CameraConnectAgent.exe"

if (-not (Test-Path -LiteralPath $uiExe)) {
    throw "UI executable not found: $uiExe"
}
if (-not (Test-Path -LiteralPath $agentExe)) {
    throw "Agent executable not found: $agentExe"
}
New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null

$resolvedLocalAppData = ""
if (-not [string]::IsNullOrWhiteSpace($LocalAppDataPath)) {
    if ([System.IO.Path]::IsPathRooted($LocalAppDataPath)) {
        $resolvedLocalAppData = $LocalAppDataPath
    } else {
        $resolvedLocalAppData = Join-Path $ProjectRoot $LocalAppDataPath
    }
} elseif ($env:LOCALAPPDATA) {
    $resolvedLocalAppData = $env:LOCALAPPDATA
} else {
    $resolvedLocalAppData = Join-Path $resolvedOutputDir "localappdata"
}
New-Item -ItemType Directory -Force -Path $resolvedLocalAppData | Out-Null
$env:LOCALAPPDATA = $resolvedLocalAppData

$startedAgentByScript = $false
$startedAgentPid = 0
$startedAgentJobId = 0

$resolvedHostName = ""
if (-not [string]::IsNullOrWhiteSpace($env:COMPUTERNAME)) {
    $resolvedHostName = $env:COMPUTERNAME.Trim()
} else {
    try {
        $resolvedHostName = [System.Net.Dns]::GetHostName()
    } catch {
        $resolvedHostName = "UNKNOWN-HOST"
    }
}

function Get-JsonBlock {
    param([Parameter(Mandatory = $true)][string]$Raw)

    $start = $Raw.IndexOf("{")
    $end = $Raw.LastIndexOf("}")
    if ($start -lt 0 -or $end -lt $start) {
        throw "No JSON object found in output: $Raw"
    }
    return $Raw.Substring($start, ($end - $start + 1))
}

function Invoke-UIAction {
    param(
        [Parameter(Mandatory = $true)][string]$Action,
        [string]$Payload = ""
    )

    $args = @("--action", $Action, "--pipe", $PipeName)
    if (-not [string]::IsNullOrWhiteSpace($Payload)) {
        $args += @("--payload", $Payload)
    }

    $raw = (& $uiExe @args 2>&1 | Out-String)
    $json = Get-JsonBlock -Raw $raw
    $obj = $json | ConvertFrom-Json -Depth 20

    [PSCustomObject]@{
        Raw    = $raw.Trim()
        Parsed = $obj
    }
}

function Test-AgentReachable {
    try {
        $ping = Invoke-UIAction -Action "ping"
        return [bool]($ping.Parsed.ok -and $ping.Parsed.success)
    } catch {
        return $false
    }
}

function Ensure-AgentRunning {
    if (Test-AgentReachable) {
        return
    }
    if ($NoAutoStartAgent) {
        throw "Agent is offline and -NoAutoStartAgent was specified."
    }

    try {
        $proc = Start-Process -FilePath $agentExe -ArgumentList "--minimized" -PassThru
        $script:startedAgentByScript = $true
        $script:startedAgentPid = $proc.Id
    } catch {
        try {
            $job = Start-Job -ScriptBlock {
                param([string]$ExePath, [string]$LocalAppData)
                $env:LOCALAPPDATA = $LocalAppData
                & $ExePath --minimized
            } -ArgumentList $agentExe, $resolvedLocalAppData

            $script:startedAgentByScript = $true
            $script:startedAgentPid = 0
            $script:startedAgentJobId = $job.Id
        } catch {
            throw "Failed to start Agent automatically: $($_.Exception.Message)"
        }
    }

    for ($i = 0; $i -lt 25; $i++) {
        Start-Sleep -Milliseconds 250
        if (Test-AgentReachable) {
            return
        }
    }

    throw "Agent did not become reachable after startup."
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

function Get-Percentile {
    param(
        [Parameter(Mandatory = $true)][double[]]$Values,
        [Parameter(Mandatory = $true)][double]$Percent
    )

    if ($Values.Count -eq 0) {
        return 0.0
    }

    $sorted = $Values | Sort-Object
    $rank = [math]::Ceiling(($Percent / 100.0) * $sorted.Count) - 1
    if ($rank -lt 0) { $rank = 0 }
    if ($rank -ge $sorted.Count) { $rank = $sorted.Count - 1 }
    return [double]$sorted[$rank]
}

function New-RunStamp {
    return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
}

function Run-Snapshot {
    Ensure-AgentRunning

    $status = Invoke-UIAction -Action "status"
    $config = Invoke-UIAction -Action "get-config"
    $backups = Invoke-UIAction -Action "get-backups"
    $logs = Invoke-UIAction -Action "subscribe-logs" -Payload '{"after_id":0,"limit":80}'

    $snapshot = [ordered]@{
        created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
        host           = $resolvedHostName
        local_appdata  = $resolvedLocalAppData
        pipe           = $PipeName
        status         = $status.Parsed
        config         = $config.Parsed
        backups        = $backups.Parsed
        logs           = $logs.Parsed
    }

    $outPath = Join-Path $resolvedOutputDir ("snapshot-{0}-{1}.json" -f $resolvedHostName, (New-RunStamp))
    $snapshot | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $outPath -Encoding UTF8
    Write-Host "[e2e] snapshot => $outPath"
}

function Run-Latency {
    Ensure-AgentRunning

    # Warm-up requests.
    for ($i = 0; $i -lt 5; $i++) {
        [void](Invoke-UIAction -Action "status")
        Start-Sleep -Milliseconds 20
    }

    $latencies = New-Object System.Collections.Generic.List[double]
    $failures = New-Object System.Collections.Generic.List[object]

    for ($i = 0; $i -lt $Iterations; $i++) {
        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-UIAction -Action "status"
        $sw.Stop()

        $elapsedMs = [double]$sw.Elapsed.TotalMilliseconds
        $latencies.Add($elapsedMs) | Out-Null

        if (-not ($result.Parsed.ok -and $result.Parsed.success)) {
            $failures.Add([ordered]@{
                iteration = $i + 1
                latency_ms = [math]::Round($elapsedMs, 3)
                code      = [string]$result.Parsed.code
                error     = [string]$result.Parsed.error
            }) | Out-Null
        }

        if ($IntervalMs -gt 0) {
            Start-Sleep -Milliseconds $IntervalMs
        }
    }

    $latArray = @($latencies.ToArray())
    $p50 = Get-Percentile -Values $latArray -Percent 50
    $p95 = Get-Percentile -Values $latArray -Percent 95
    $p99 = Get-Percentile -Values $latArray -Percent 99
    $avg = 0.0
    if ($latArray.Count -gt 0) {
        $avg = ($latArray | Measure-Object -Average).Average
    }

    $report = [ordered]@{
        created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
        host           = $resolvedHostName
        local_appdata  = $resolvedLocalAppData
        iterations     = $Iterations
        interval_ms    = $IntervalMs
        target_p95_ms  = $P95TargetMs
        stats = [ordered]@{
            min_ms = [math]::Round(($latArray | Measure-Object -Minimum).Minimum, 3)
            p50_ms = [math]::Round($p50, 3)
            p95_ms = [math]::Round($p95, 3)
            p99_ms = [math]::Round($p99, 3)
            max_ms = [math]::Round(($latArray | Measure-Object -Maximum).Maximum, 3)
            avg_ms = [math]::Round([double]$avg, 3)
        }
        failures   = @($failures.ToArray())
        pass       = ($failures.Count -eq 0 -and $p95 -le $P95TargetMs)
    }

    $outPath = Join-Path $resolvedOutputDir ("latency-{0}-{1}.json" -f $resolvedHostName, (New-RunStamp))
    $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $outPath -Encoding UTF8

    Write-Host ("[e2e] latency p95={0}ms, failures={1}, pass={2}" -f `
        $report.stats.p95_ms, $failures.Count, $report.pass)
    Write-Host "[e2e] latency => $outPath"
}

try {
    switch ($Mode) {
        "latency" {
            Run-Latency
        }
        "snapshot" {
            Run-Snapshot
        }
        "all" {
            Run-Snapshot
            Run-Latency
        }
        default {
            throw "Unsupported mode: $Mode"
        }
    }
} finally {
    Stop-AgentIfOwned
}
