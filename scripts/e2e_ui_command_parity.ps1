param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDir = "build/e2e",
    [string]$UIExePath = "",
    [string]$PipeName = "\\.\pipe\CameraConnectIPC"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-InputPath {
    param(
        [string]$Root,
        [string]$Path
    )
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $Path
    }
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return $Path
    }
    return (Join-Path $Root $Path)
}

function Resolve-UIExe {
    param(
        [string]$Root,
        [string]$Provided
    )
    if (-not [string]::IsNullOrWhiteSpace($Provided)) {
        $resolved = Resolve-InputPath -Root $Root -Path $Provided
        if (Test-Path -LiteralPath $resolved) {
            return (Resolve-Path -LiteralPath $resolved).Path
        }
        throw "UI executable not found: $resolved"
    }

    $candidates = @(
        (Join-Path $Root "build/bin/CameraConnectUI.exe"),
        (Join-Path $Root "ui.exe")
    )

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }

    throw "Unable to resolve UI executable. Checked: $($candidates -join ', ')"
}

function Get-JsonBlock {
    param([string]$Raw)

    $ansiPattern = [char]27 + '\[[0-9;]*[A-Za-z]'
    $sanitized = [regex]::Replace($Raw, $ansiPattern, "")
    $trimmed = $sanitized.Trim()
    $start = $trimmed.IndexOf("{")
    if ($start -lt 0) {
        throw "No JSON block found in command output."
    }

    for ($end = $trimmed.Length - 1; $end -ge $start; $end--) {
        if ($trimmed[$end] -ne "}") { continue }
        $candidate = $trimmed.Substring($start, ($end - $start + 1))
        try {
            $null = $candidate | ConvertFrom-Json -ErrorAction Stop
            return $candidate
        } catch {
        }
    }

    throw "Unable to parse JSON block from command output."
}

function Invoke-UIAction {
    param(
        [string]$UIExe,
        [string]$Pipe,
        [string]$Action,
        [string]$Payload = ""
    )

    $args = @("--action", $Action, "--pipe", $Pipe)
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
        throw "Failed to parse JSON output for action '$Action'. Raw output:`n$raw"
    }
    $parsed = $json | ConvertFrom-Json

    $requiredKeys = @("ok", "success", "code", "server_ts")
    $presentKeys = @($parsed.PSObject.Properties.Name)
    $missing = @($requiredKeys | Where-Object { $presentKeys -notcontains $_ })
    $hasEnvelope = ($missing.Count -eq 0)

    return [PSCustomObject]@{
        action        = $Action
        payload       = $Payload
        raw_output    = $raw.Trim()
        parsed        = $parsed
        has_envelope  = $hasEnvelope
        missing_keys  = $missing
    }
}

function New-RunStamp {
    return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
}

$resolvedOutputDir = Resolve-InputPath -Root $ProjectRoot -Path $OutputDir
New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null

$uiExe = Resolve-UIExe -Root $ProjectRoot -Provided $UIExePath

$actions = @(
    @{ action = "ping"; payload = "" },
    @{ action = "status"; payload = "" },
    @{ action = "get-config"; payload = "" },
    @{ action = "save-config"; payload = "{}" },
    @{ action = "get-backups"; payload = "" }
)

$results = New-Object System.Collections.Generic.List[object]
foreach ($item in $actions) {
    $results.Add((Invoke-UIAction -UIExe $uiExe -Pipe $PipeName -Action $item.action -Payload $item.payload)) | Out-Null
}

$allEnvelope = @($results | ForEach-Object { [bool]$_.has_envelope })
$overallPass = ($allEnvelope -notcontains $false)

$report = [ordered]@{
    created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    ui_exe         = $uiExe
    pipe           = $PipeName
    checks = [ordered]@{
        all_commands_have_envelope = $overallPass
    }
    results        = $results
    pass           = $overallPass
}

$outPath = Join-Path $resolvedOutputDir ("ui-command-parity-{0}.json" -f (New-RunStamp))
$report | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $outPath -Encoding UTF8

Write-Host "[ui-parity] report => $outPath"
Write-Host "[ui-parity] pass=$overallPass"

if (-not $overallPass) {
    exit 1
}
