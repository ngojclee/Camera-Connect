param(
    [string]$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDir = "build/e2e",
    [string]$SnapshotA = "",
    [string]$SnapshotB = "",
    [string]$LatencyA = "",
    [string]$LatencyB = "",
    [int]$MinimumBackupOverlap = 1
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$resolvedOutputDir = Join-Path $ProjectRoot $OutputDir
New-Item -ItemType Directory -Force -Path $resolvedOutputDir | Out-Null

function Resolve-InputPath {
    param(
        [string]$ProjectRoot,
        [string]$Path
    )
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $Path
    }
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return $Path
    }
    return (Join-Path $ProjectRoot $Path)
}

function Resolve-SnapshotPair {
    param(
        [string]$ProjectRoot,
        [string]$OutputDir,
        [string]$SnapshotA,
        [string]$SnapshotB
    )

    if ($SnapshotA -and $SnapshotB) {
        $resolvedSnapshotA = Resolve-InputPath -ProjectRoot $ProjectRoot -Path $SnapshotA
        $resolvedSnapshotB = Resolve-InputPath -ProjectRoot $ProjectRoot -Path $SnapshotB
        if (-not (Test-Path -LiteralPath $resolvedSnapshotA)) {
            throw "SnapshotA not found: $resolvedSnapshotA"
        }
        if (-not (Test-Path -LiteralPath $resolvedSnapshotB)) {
            throw "SnapshotB not found: $resolvedSnapshotB"
        }
        return @(
            (Resolve-Path -LiteralPath $resolvedSnapshotA).Path,
            (Resolve-Path -LiteralPath $resolvedSnapshotB).Path
        )
    }

    $all = Get-ChildItem -Path $OutputDir -Filter "snapshot-*.json" -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending
    if ($all.Count -lt 2) {
        throw "Need at least two snapshot files in $OutputDir (or pass -SnapshotA/-SnapshotB)."
    }

    return @($all[0].FullName, $all[1].FullName)
}

function Load-JsonFile {
    param([string]$Path)
    return (Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json -Depth 30)
}

function Get-PropValue {
    param(
        $Object,
        [string]$Name,
        $Default = $null
    )
    if ($null -eq $Object) {
        return $Default
    }
    $prop = $Object.PSObject.Properties[$Name]
    if ($prop) {
        return $prop.Value
    }
    return $Default
}

function Get-BackupNames {
    param($Snapshot)

    $names = New-Object System.Collections.Generic.List[string]
    $backupsSection = Get-PropValue -Object $Snapshot -Name "backups"
    $backupData = Get-PropValue -Object $backupsSection -Name "data"
    if ($null -eq $backupData) {
        return @($names.ToArray())
    }

    foreach ($item in @($backupData)) {
        if ($null -eq $item) { continue }
        $catalogName = Get-PropValue -Object $item -Name "catalog_name"
        if (-not [string]::IsNullOrWhiteSpace([string]$catalogName)) {
            $names.Add(([string]$catalogName).Trim()) | Out-Null
            continue
        }
        $pathValue = Get-PropValue -Object $item -Name "path"
        if (-not [string]::IsNullOrWhiteSpace([string]$pathValue)) {
            $path = [string]$pathValue
            $leaf = [System.IO.Path]::GetFileNameWithoutExtension($path)
            if (-not [string]::IsNullOrWhiteSpace($leaf)) {
                $names.Add($leaf.Trim()) | Out-Null
            }
        }
    }
    return @($names.ToArray() | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Sort-Object -Unique)
}

function Test-CallSuccess {
    param($Section)
    if (-not $Section) { return $false }
    $ok = Get-PropValue -Object $Section -Name "ok" -Default $false
    $success = Get-PropValue -Object $Section -Name "success" -Default $false
    return [bool]($ok -and $success)
}

function Test-LockStatusParsable {
    param($Snapshot)
    $statusSection = Get-PropValue -Object $Snapshot -Name "status"
    $statusData = Get-PropValue -Object $statusSection -Name "data"
    if ($null -eq $statusData) {
        return $false
    }
    $raw = [string](Get-PropValue -Object $statusData -Name "lock_status" -Default "")
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return $true
    }
    switch ($raw.ToUpperInvariant()) {
        "ONLINE" { return $true }
        "OFFLINE" { return $true }
        "ERROR" { return $true }
        default { return $false }
    }
}

function Get-LatencyPass {
    param(
        [string]$ProjectRoot,
        [string]$Path
    )
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $null
    }
    $resolvedPath = Resolve-InputPath -ProjectRoot $ProjectRoot -Path $Path
    if (-not (Test-Path -LiteralPath $resolvedPath)) {
        throw "Latency report not found: $resolvedPath"
    }
    $obj = Load-JsonFile -Path $resolvedPath
    return [bool]$obj.pass
}

function New-Stamp {
    return (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssfffZ")
}

$pair = Resolve-SnapshotPair -ProjectRoot $ProjectRoot -OutputDir $resolvedOutputDir -SnapshotA $SnapshotA -SnapshotB $SnapshotB
$pathA = $pair[0]
$pathB = $pair[1]

$snapA = Load-JsonFile -Path $pathA
$snapB = Load-JsonFile -Path $pathB

$backupsA = Get-BackupNames -Snapshot $snapA
$backupsB = Get-BackupNames -Snapshot $snapB
$overlap = @($backupsA | Where-Object { $backupsB -contains $_ })

$latencyPassA = Get-LatencyPass -ProjectRoot $ProjectRoot -Path $LatencyA
$latencyPassB = Get-LatencyPass -ProjectRoot $ProjectRoot -Path $LatencyB

$checks = [ordered]@{
    distinct_hosts               = ([string]$snapA.host -ne [string]$snapB.host)
    machine_a_status_call_ok     = (Test-CallSuccess -Section $snapA.status)
    machine_b_status_call_ok     = (Test-CallSuccess -Section $snapB.status)
    machine_a_config_call_ok     = (Test-CallSuccess -Section $snapA.config)
    machine_b_config_call_ok     = (Test-CallSuccess -Section $snapB.config)
    machine_a_backups_call_ok    = (Test-CallSuccess -Section $snapA.backups)
    machine_b_backups_call_ok    = (Test-CallSuccess -Section $snapB.backups)
    machine_a_lock_status_valid  = (Test-LockStatusParsable -Snapshot $snapA)
    machine_b_lock_status_valid  = (Test-LockStatusParsable -Snapshot $snapB)
    backup_overlap_meets_minimum = ($overlap.Count -ge $MinimumBackupOverlap)
}

if ($null -ne $latencyPassA) {
    $checks.machine_a_latency_pass = [bool]$latencyPassA
}
if ($null -ne $latencyPassB) {
    $checks.machine_b_latency_pass = [bool]$latencyPassB
}

$allChecks = @($checks.GetEnumerator() | ForEach-Object { [bool]$_.Value })
$overallPass = ($allChecks -notcontains $false)

$report = [ordered]@{
    created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    snapshot_a     = $pathA
    snapshot_b     = $pathB
    host_a         = [string]$snapA.host
    host_b         = [string]$snapB.host
    overlap = [ordered]@{
        minimum_required = $MinimumBackupOverlap
        count            = $overlap.Count
        names            = $overlap
    }
    checks  = $checks
    pass    = $overallPass
}

$stamp = New-Stamp
$jsonOut = Join-Path $resolvedOutputDir ("two-machine-compare-{0}.json" -f $stamp)
$mdOut = Join-Path $resolvedOutputDir ("two-machine-compare-{0}.md" -f $stamp)

$report | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $jsonOut -Encoding UTF8

$mdLines = New-Object System.Collections.Generic.List[string]
$mdLines.Add("# Two-Machine E2E Compare Report") | Out-Null
$mdLines.Add("") | Out-Null
$mdLines.Add(('- Generated UTC: {0}' -f $report.created_at_utc)) | Out-Null
$mdLines.Add(('- Snapshot A: `{0}`' -f $pathA)) | Out-Null
$mdLines.Add(('- Snapshot B: `{0}`' -f $pathB)) | Out-Null
$mdLines.Add(('- Overall Pass: **{0}**' -f $overallPass)) | Out-Null
$mdLines.Add("") | Out-Null
$mdLines.Add("| Check | Result |") | Out-Null
$mdLines.Add("|---|---|") | Out-Null
foreach ($entry in $checks.GetEnumerator()) {
    $icon = if ([bool]$entry.Value) { "PASS" } else { "FAIL" }
    $mdLines.Add(("| {0} | {1} |" -f $entry.Key, $icon)) | Out-Null
}
$mdLines.Add("") | Out-Null
$mdLines.Add(("Backup overlap count: **{0}**" -f $overlap.Count)) | Out-Null
if ($overlap.Count -gt 0) {
    foreach ($name in $overlap) {
        $mdLines.Add(("- {0}" -f $name)) | Out-Null
    }
}

$mdLines -join "`r`n" | Set-Content -LiteralPath $mdOut -Encoding UTF8

Write-Host "[e2e-compare] JSON => $jsonOut"
Write-Host "[e2e-compare] MD   => $mdOut"
if ($overallPass) {
    Write-Host "[e2e-compare] PASS"
    exit 0
}

Write-Host "[e2e-compare] FAIL"
exit 1
