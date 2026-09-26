//go:build windows

package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options configures tray bootstrap behavior.
type Options struct {
	AppName      string
	AgentPID     int
	UIExecutable string
	PipeName     string
	StatusPath   string
}

// Manager hosts a lightweight tray process via PowerShell NotifyIcon.
type Manager struct {
	opts Options

	mu  sync.Mutex
	cmd *exec.Cmd
}

// NewManager creates a Windows tray manager.
func NewManager(opts Options) *Manager {
	if strings.TrimSpace(opts.AppName) == "" {
		opts.AppName = "Camera Connect"
	}
	if opts.AgentPID <= 0 {
		opts.AgentPID = -1
	}
	return &Manager{opts: opts}
}

// Start launches tray host as detached PowerShell process.
func (m *Manager) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		return nil
	}

	script := renderPowerShellTrayScript(m.opts)

	// Write script to file — more reliable than -EncodedCommand which can
	// silently fail due to encoding issues or command line length limits.
	scriptDir := filepath.Dir(m.opts.StatusPath)
	if scriptDir == "" || scriptDir == "." {
		scriptDir = os.TempDir()
	}
	_ = os.MkdirAll(scriptDir, 0o755)
	scriptPath := filepath.Join(scriptDir, "tray-host.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return fmt.Errorf("write tray script: %w", err)
	}

	var lastErr error
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		cmd := exec.Command(
			shell,
			"-Sta",
			"-NoProfile",
			"-ExecutionPolicy",
			"Bypass",
			"-WindowStyle", "Hidden",
			"-File",
			scriptPath,
		)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}

		// Detect scripts that exit immediately (common when WinForms fails to initialize).
		exitCh := make(chan error, 1)
		go func() {
			exitCh <- cmd.Wait()
		}()
		select {
		case err := <-exitCh:
			lastErr = fmt.Errorf("%s exited early: %w", shell, err)
			continue
		case <-time.After(2000 * time.Millisecond):
		}

		m.cmd = cmd
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("launch tray host: %w", lastErr)
	}
	return fmt.Errorf("launch tray host: unknown error")
}

// Stop terminates tray host process if still running.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	err := m.cmd.Process.Kill()
	m.cmd = nil
	return err
}

func psSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func renderPowerShellTrayScript(opts Options) string {
	appName := psSingleQuote(opts.AppName)
	uiExe := psSingleQuote(opts.UIExecutable)
	pipe := psSingleQuote(opts.PipeName)
	statusPath := psSingleQuote(opts.StatusPath)

	return fmt.Sprintf(`
$ErrorActionPreference = 'SilentlyContinue'

$AppName = '%s'
$AgentPid = %d
$UIExe = '%s'
$PipeName = '%s'
$StatusFile = '%s'
$TrayDir = [System.IO.Path]::GetDirectoryName($StatusFile)
if ([string]::IsNullOrWhiteSpace($TrayDir)) {
    $TrayDir = $env:TEMP
}

# Ensure log directory exists before anything else
try { [System.IO.Directory]::CreateDirectory($TrayDir) | Out-Null } catch {}

$TrayLog = [System.IO.Path]::Combine($TrayDir, 'tray-host.log')

function Write-TrayLog([string]$message) {
    try {
        $line = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss') + ' ' + $message
        [System.IO.File]::AppendAllText($TrayLog, $line + [Environment]::NewLine)
    } catch {}
}

Write-TrayLog ('tray host booting (ui=' + $UIExe + ', pid=' + $AgentPid + ')')

# Load WinForms assemblies with error capture
try {
    Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
    Add-Type -AssemblyName System.Drawing -ErrorAction Stop
    Write-TrayLog 'assemblies loaded OK'
} catch {
    Write-TrayLog ('FATAL: assembly load failed: ' + $_.Exception.Message)
    Start-Sleep -Seconds 60
    exit 1
}

[System.Windows.Forms.Application]::EnableVisualStyles()

function Invoke-UiAction([string]$action) {
    if ([string]::IsNullOrWhiteSpace($UIExe)) { return }
    try {
        Start-Process -FilePath $UIExe -ArgumentList @('--action', $action, '--pipe', $PipeName) -WindowStyle Hidden | Out-Null
    } catch {}
}

# Resolve best icon — try UI exe, then agent exe, then system fallback
$resolvedIcon = $null
$iconCandidates = @()
if (-not [string]::IsNullOrWhiteSpace($UIExe)) { $iconCandidates += $UIExe }
# Also try agent exe in same directory
$agentDir = [System.IO.Path]::GetDirectoryName($UIExe)
if (-not [string]::IsNullOrWhiteSpace($agentDir)) {
    $iconCandidates += [System.IO.Path]::Combine($agentDir, 'CameraConnectAgent.exe')
}
foreach ($candidate in $iconCandidates) {
    try {
        if (Test-Path $candidate) {
            $resolvedIcon = [System.Drawing.Icon]::ExtractAssociatedIcon($candidate)
            if ($null -ne $resolvedIcon) {
                Write-TrayLog ('icon extracted from: ' + $candidate)
                break
            }
        }
    } catch {
        Write-TrayLog ('icon extract failed for: ' + $candidate + ' - ' + $_.Exception.Message)
    }
}
if ($null -eq $resolvedIcon) {
    $resolvedIcon = [System.Drawing.SystemIcons]::Application
    Write-TrayLog 'using fallback system icon'
}

$notify = New-Object System.Windows.Forms.NotifyIcon
$notify.Text = $AppName
$notify.Icon = $resolvedIcon
$notify.BalloonTipTitle = $AppName
$notify.BalloonTipText = 'Agent is running'
# Force visibility with standard Windows workaround
$notify.Visible = $true
$notify.Visible = $false
Start-Sleep -Milliseconds 100
$notify.Visible = $true
Write-TrayLog 'notify icon visible=true'

$menu = New-Object System.Windows.Forms.ContextMenuStrip
$statusItem = New-Object System.Windows.Forms.ToolStripMenuItem('Status: starting...')
$statusItem.Enabled = $false
$menu.Items.Add($statusItem) | Out-Null
$menu.Items.Add('-') | Out-Null

$openUI = New-Object System.Windows.Forms.ToolStripMenuItem('Open UI')
$openUI.Add_Click({
    try {
        if (-not [string]::IsNullOrWhiteSpace($UIExe)) {
            Start-Process -FilePath $UIExe | Out-Null
        }
    } catch {}
})
$menu.Items.Add($openUI) | Out-Null

$syncNow = New-Object System.Windows.Forms.ToolStripMenuItem('Sync Now')
$syncNow.Add_Click({ Invoke-UiAction 'sync-now' })
$menu.Items.Add($syncNow) | Out-Null

$menu.Items.Add('-') | Out-Null
$exitItem = New-Object System.Windows.Forms.ToolStripMenuItem('Exit App')
$exitItem.Add_Click({
    try {
        if (($AgentPid -as [int]) -gt 0) {
            Stop-Process -Id $AgentPid -Force -ErrorAction SilentlyContinue
        }
        Stop-Process -Name 'CameraConnect' -Force -ErrorAction SilentlyContinue
    } catch {}
})
$menu.Items.Add($exitItem) | Out-Null

$notify.ContextMenuStrip = $menu
$notify.Add_DoubleClick({
    try {
        if (-not [string]::IsNullOrWhiteSpace($UIExe)) {
            Start-Process -FilePath $UIExe | Out-Null
        }
    } catch {}
})

function Get-BadgedIcon {
    param([System.Drawing.Icon]$BaseIcon, [string]$HtmlColor)
    try {
        $bmp = New-Object System.Drawing.Bitmap 16,16
        $g = [System.Drawing.Graphics]::FromImage($bmp)
        $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $rect = [System.Drawing.Rectangle]::new(0,0,16,16)
        $g.DrawIcon($BaseIcon, $rect)
        
        $color = [System.Drawing.ColorTranslator]::FromHtml($HtmlColor)
        $brush = [System.Drawing.SolidBrush]::new($color)
        $pen = [System.Drawing.Pen]::new([System.Drawing.Color]::Black, 1)
        
        $g.FillEllipse($brush, 9, 9, 6, 6)
        $g.DrawEllipse($pen, 9, 9, 6, 6)
        
        $brush.Dispose()
        $pen.Dispose()
        $g.Dispose()
        $ptr = $bmp.GetHicon()
        return [System.Drawing.Icon]::FromHandle($ptr)
    } catch {
        return $BaseIcon
    }
}
$OriginalIcon = $notify.Icon

$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = 1500
$timer.Add_Tick({
    if (($AgentPid -as [int]) -gt 0) {
        $alive = Get-Process -Id $AgentPid -ErrorAction SilentlyContinue
        if (-not $alive) {
            Write-TrayLog 'agent process not found; shutting tray host'
            $timer.Stop()
            $notify.Visible = $false
            $notify.Dispose()
            [System.Windows.Forms.Application]::Exit()
            return
        }
    }

    if (Test-Path $StatusFile) {
        try {
            $raw = Get-Content -LiteralPath $StatusFile -Raw
            $obj = $raw | ConvertFrom-Json
            if ($obj -and $obj.status_text) {
                $statusText = [string]$obj.status_text
                if ($statusText.Length -gt 40) {
                    $statusText = $statusText.Substring(0, 40) + '...'
                }
                $statusItem.Text = 'Status: ' + $statusText
                $tooltipText = $AppName + ' - ' + $statusText
                if ($tooltipText.Length -gt 63) { $tooltipText = $tooltipText.Substring(0, 63) }
                $notify.Text = $tooltipText
                
                if ($obj.tray_color) {
                    $tc = [string]$obj.tray_color
                    if ($tc -eq 'green') {
                        $notify.Icon = Get-BadgedIcon -BaseIcon $OriginalIcon -HtmlColor '#00FF00'
                    } elseif ($tc -eq 'blue') {
                        $notify.Icon = Get-BadgedIcon -BaseIcon $OriginalIcon -HtmlColor '#00BFFF'
                    } elseif ($tc -eq 'yellow' -or $tc -eq 'orange') {
                        $notify.Icon = Get-BadgedIcon -BaseIcon $OriginalIcon -HtmlColor '#FFA500'
                    } elseif ($tc -eq 'red') {
                        $notify.Icon = Get-BadgedIcon -BaseIcon $OriginalIcon -HtmlColor '#FF0000'
                    } else {
                        $notify.Icon = $OriginalIcon
                    }
                }
            }
        } catch {}
    }
})
$timer.Start()

Write-TrayLog 'entering WinForms message loop'
[System.Windows.Forms.Application]::Run()
Write-TrayLog 'tray host exited'
$notify.Visible = $false
$notify.Dispose()
`, appName, opts.AgentPID, uiExe, pipe, statusPath)
}
