package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ngojclee/camera-connect/internal/ipc"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Service is the UI/CLI-side facade that sends IPC commands to the Agent.
// It performs no disk or network work itself — the Agent owns all state.
type Service struct {
	pipeName string

	// Injectable seams for tests.
	call   func(context.Context, string, ipc.Request) (ipc.Response, error)
	now    func() time.Time
	ctxFor func(time.Duration) (context.Context, context.CancelFunc)
}

// NewService creates an IPC client bound to a pipe name.
func NewService(pipeName string) *Service {
	if strings.TrimSpace(pipeName) == "" {
		pipeName = ipc.PipeName
	}
	return &Service{
		pipeName: pipeName,
		call:     ipc.Call,
		now:      time.Now,
		ctxFor: func(d time.Duration) (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), d)
		},
	}
}

func (s *Service) request(command ipc.CommandType, payload any, timeout time.Duration) (ipc.Response, error) {
	ctx, cancel := s.ctxFor(timeout)
	defer cancel()
	return s.call(ctx, s.pipeName, ipc.Request{Command: command, Payload: payload})
}

func (s *Service) envelope(command ipc.CommandType, payload any, timeout time.Duration) ActionEnvelope {
	resp, err := s.request(command, payload, timeout)
	if err != nil {
		return agentOfflineEnvelope(s.now, err)
	}
	return responseEnvelope(s.now, resp)
}

// ---------- typed commands ----------

// Ping checks whether the Agent is alive.
func (s *Service) Ping() ActionEnvelope {
	resp, err := s.request(ipc.CmdPing, nil, 1500*time.Millisecond)
	if err != nil {
		return agentOfflineEnvelope(s.now, err)
	}
	return responseEnvelope(s.now, resp)
}

// GetStatus fetches the full AppStatus snapshot.
func (s *Service) GetStatus() ActionEnvelope {
	return s.envelope(ipc.CmdGetStatus, nil, 3*time.Second)
}

// GetConfig fetches the resolved ConfigSnapshot.
func (s *Service) GetConfig() ActionEnvelope {
	return s.envelope(ipc.CmdGetConfig, nil, 3*time.Second)
}

// SaveConfig applies a partial config patch (JSON string payload).
func (s *Service) SaveConfig(payloadJSON string) ActionEnvelope {
	var patch ipc.SaveConfigPayload
	if err := json.Unmarshal([]byte(payloadJSON), &patch); err != nil {
		return badRequestEnvelope(s.now, fmt.Sprintf("invalid save_config payload: %v", err))
	}
	return s.envelope(ipc.CmdSaveConfig, patch, 5*time.Second)
}

// ListCameras returns currently detected cameras/drives.
func (s *Service) ListCameras() ActionEnvelope {
	return s.envelope(ipc.CmdListCameras, nil, 3*time.Second)
}

// ScanNow requests a manual scan. payloadJSON is optional ScanNowPayload.
func (s *Service) ScanNow(payloadJSON string) ActionEnvelope {
	var payload ipc.ScanNowPayload
	if strings.TrimSpace(payloadJSON) != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return badRequestEnvelope(s.now, fmt.Sprintf("invalid scan_now payload: %v", err))
		}
	}
	return s.envelope(ipc.CmdScanNow, payload, 5*time.Second)
}

// SetProfile switches the active profile.
func (s *Service) SetProfile(profileID string) ActionEnvelope {
	if strings.TrimSpace(profileID) == "" {
		return badRequestEnvelope(s.now, "profile_id is required")
	}
	return s.envelope(ipc.CmdSetProfile, ipc.SetProfilePayload{ProfileID: profileID}, 3*time.Second)
}

// SubscribeLogs fetches buffered log entries.
func (s *Service) SubscribeLogs(afterID int64, limit int, level string) ActionEnvelope {
	return s.envelope(ipc.CmdSubscribeLogs, ipc.SubscribeLogsPayload{
		AfterID: afterID,
		Limit:   limit,
		Level:   level,
	}, 5*time.Second)
}

// PauseSync pauses the sync worker.
func (s *Service) PauseSync() ActionEnvelope {
	return s.envelope(ipc.CmdPauseSync, nil, 3*time.Second)
}

// ResumeSync resumes the sync worker.
func (s *Service) ResumeSync() ActionEnvelope {
	return s.envelope(ipc.CmdResumeSync, nil, 3*time.Second)
}

// BackupStatus fetches the upload queue state.
func (s *Service) BackupStatus() ActionEnvelope {
	return s.envelope(ipc.CmdBackupStatus, nil, 3*time.Second)
}

// RetryBackups re-queues failed uploads.
func (s *Service) RetryBackups() ActionEnvelope {
	return s.envelope(ipc.CmdRetryBackups, nil, 5*time.Second)
}

// CheckUpdate asks the Agent to check GitHub releases.
func (s *Service) CheckUpdate() ActionEnvelope {
	return s.envelope(ipc.CmdCheckUpdate, nil, 30*time.Second)
}

// DownloadUpdate asks the Agent to download a release asset.
func (s *Service) DownloadUpdate(payloadJSON string) ActionEnvelope {
	var payload ipc.DownloadUpdatePayload
	if strings.TrimSpace(payloadJSON) != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return badRequestEnvelope(s.now, fmt.Sprintf("invalid download_update payload: %v", err))
		}
	}
	return s.envelope(ipc.CmdDownloadUpdate, payload, 10*time.Second)
}

// ShutdownAgent asks the Agent process to exit.
func (s *Service) ShutdownAgent() ActionEnvelope {
	return s.envelope(ipc.CmdShutdownAgent, nil, 3*time.Second)
}

// AppDBStatus fetches cloud-sync account state.
func (s *Service) AppDBStatus() ActionEnvelope {
	return s.envelope(ipc.CmdAppDBStatus, nil, 3*time.Second)
}

// AppDBLogin logs into appdb.lengoc.me via the Agent.
func (s *Service) AppDBLogin(email, password string) ActionEnvelope {
	if strings.TrimSpace(email) == "" || password == "" {
		return badRequestEnvelope(s.now, "email and password are required")
	}
	return s.envelope(ipc.CmdAppDBLogin, ipc.AppDBLoginPayload{Email: email, Password: password}, 30*time.Second)
}

// AppDBLogout clears AppDB session state.
func (s *Service) AppDBLogout() ActionEnvelope {
	return s.envelope(ipc.CmdAppDBLogout, nil, 10*time.Second)
}

// VaultPush seals and uploads the local rclone.conf to AppDB.
func (s *Service) VaultPush(passphrase string) ActionEnvelope {
	if passphrase == "" {
		return badRequestEnvelope(s.now, "passphrase is required")
	}
	return s.envelope(ipc.CmdVaultPush, ipc.VaultPayload{Passphrase: passphrase}, 30*time.Second)
}

// VaultPull downloads and unseals the rclone bundle from AppDB.
func (s *Service) VaultPull(passphrase string) ActionEnvelope {
	if passphrase == "" {
		return badRequestEnvelope(s.now, "passphrase is required")
	}
	return s.envelope(ipc.CmdVaultPull, ipc.VaultPayload{Passphrase: passphrase}, 30*time.Second)
}

// ---------- generic dispatch ----------

// ExecuteAction maps a CLI/JS action name to a typed command.
func (s *Service) ExecuteAction(action, payload string) ActionEnvelope {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "ping":
		return s.Ping()
	case "get-status", "status":
		return s.GetStatus()
	case "get-config", "config":
		return s.GetConfig()
	case "save-config":
		return s.SaveConfig(payload)
	case "list-cameras", "cameras":
		return s.ListCameras()
	case "scan-now", "sync-now":
		return s.ScanNow(payload)
	case "set-profile":
		return s.SetProfile(payload)
	case "subscribe-logs", "logs":
		return s.SubscribeLogs(0, 200, "")
	case "pause-sync":
		return s.PauseSync()
	case "resume-sync":
		return s.ResumeSync()
	case "backup-status":
		return s.BackupStatus()
	case "retry-backups":
		return s.RetryBackups()
	case "check-update":
		return s.CheckUpdate()
	case "download-update":
		return s.DownloadUpdate(payload)
	case "shutdown-agent":
		return s.ShutdownAgent()
	case "appdb-status":
		return s.AppDBStatus()
	case "appdb-login":
		var p ipc.AppDBLoginPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return badRequestEnvelope(s.now, fmt.Sprintf("invalid appdb_login payload: %v", err))
		}
		return s.AppDBLogin(p.Email, p.Password)
	case "appdb-logout":
		return s.AppDBLogout()
	case "vault-push":
		return s.VaultPush(payload)
	case "vault-pull":
		return s.VaultPull(payload)
	default:
		return badRequestEnvelope(s.now, fmt.Sprintf("unknown action: %s", action))
	}
}
