param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$BinDir = "build/bin",
    [switch]$KeepAgentRunning
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$bin = Join-Path $ProjectRoot $BinDir
$agent = Join-Path $bin "CameraConnectAgent.exe"
$ui = Join-Path $bin "CameraConnect.exe"
if (-not (Test-Path $agent)) { $agent = Join-Path $ProjectRoot "build/CameraConnectAgent.exe" }
if (-not (Test-Path $ui)) { $ui = Join-Path $ProjectRoot "build/CameraConnect.exe" }
foreach ($p in @($agent, $ui)) {
    if (-not (Test-Path $p)) { throw "missing binary: $p (run scripts/build_windows.ps1 first)" }
}

$results = @()
function Check { param([string]$Name, [bool]$Ok, [string]$Detail = "")
    $script:results += [pscustomobject]@{ check = $Name; pass = $Ok; detail = $Detail }
    Write-Host ("  [{0}] {1} {2}" -f $(if ($Ok) { "PASS" } else { "FAIL" }), $Name, $Detail)
}

function Invoke-Action([string]$Action, [string]$Payload = "") {
    # Start-Process avoids NativeCommandError noise from the exe's stderr logs.
    $stdout = [IO.Path]::GetTempFileName()
    $stderr = [IO.Path]::GetTempFileName()
    $argList = @("--action", $Action)
    if ($Payload) { $argList += @("--payload", ('"' + ($Payload -replace '"', '\"') + '"')) }
    $argLine = ($argList -join " ")
    try {
        $p = Start-Process -FilePath $ui -ArgumentList $argLine -Wait -NoNewWindow `
            -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
        $out = Get-Content -LiteralPath $stdout -Raw -ErrorAction SilentlyContinue
        return ($out | ConvertFrom-Json)
    } catch { return $null } finally {
        Remove-Item $stdout, $stderr -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "== e2e smoke: agent + IPC =="
Start-Process -FilePath $agent -ArgumentList "--minimized" -WindowStyle Hidden
$agentProc = $null
Start-Sleep -Seconds 4
try {
    $agentProc = Get-Process CameraConnectAgent -ErrorAction SilentlyContinue
    Check "agent running" ($null -ne $agentProc)

    $ping = Invoke-Action "ping"
    Check "ping" ($ping -and $ping.success -eq $true) ($ping.data.message)

    $status = Invoke-Action "get-status"
    Check "get-status" ($status -and $status.success -eq $true) ("state=" + $status.data.status_text)

    $cfg = Invoke-Action "get-config"
    Check "get-config" ($cfg -and $cfg.success -eq $true) ("profiles=" + @($cfg.data.profiles).Count)

    $cams = Invoke-Action "list-cameras"
    Check "list-cameras" ($cams -and $cams.success -eq $true) ("count=" + @($cams.data).Count)

    $hist = Invoke-Action "list-history" '{"limit":5}'
    Check "list-history" ($hist -and $hist.success -eq $true)

    $logs = Invoke-Action "subscribe-logs" '{"limit":10}'
    Check "subscribe-logs" ($logs -and $logs.success -eq $true) ("entries=" + @($logs.data.entries).Count)

    $bad = Invoke-Action "bogus-command"
    Check "unknown cmd rejected" ($bad -and $bad.success -eq $false -and $bad.code -in @("unknown_command", "bad_request")) ($bad.code)
} finally {
    if (-not $KeepAgentRunning -and $agentProc) {
        $sd = Invoke-Action "shutdown-agent"
        Check "shutdown-agent" ($sd -and $sd.success -eq $true)
        Start-Sleep -Milliseconds 800
        $still = Get-Process CameraConnectAgent -ErrorAction SilentlyContinue
        Check "agent exited" ($null -eq $still)
        if ($still) { $still | Stop-Process -Force }
    }
}

$failed = @($results | Where-Object { -not $_.pass })
Write-Host ""
Write-Host ("== {0}/{1} checks passed ==" -f ($results.Count - $failed.Count), $results.Count)
if ($failed.Count -gt 0) { exit 1 }
