// Package ipc defines the contract between Agent and UI processes.
// Communication is over Windows named pipes.
package ipc

import "time"

// PipeName is the named pipe address for Agent ↔ UI communication.
const PipeName = `\\.\pipe\CameraConnectIPC`

// IPC defaults.
const (
	DefaultConnectTimeout = 1500 * time.Millisecond
	DefaultRequestTimeout = 3 * time.Second
)

// Command types sent from UI to Agent.
type CommandType string

const (
	CmdPing           CommandType = "ping"
	CmdGetStatus      CommandType = "get_status"
	CmdGetConfig      CommandType = "get_config"
	CmdSaveConfig     CommandType = "save_config"
	CmdListCameras    CommandType = "list_cameras"
	CmdScanNow        CommandType = "scan_now"
	CmdListProfiles   CommandType = "list_profiles"
	CmdSetProfile     CommandType = "set_profile"
	CmdImportDates    CommandType = "import_dates"
	CmdBackupStatus   CommandType = "backup_status"
	CmdRetryBackups   CommandType = "retry_backups"
	CmdListHistory    CommandType = "list_history"
	CmdSubscribeLogs  CommandType = "subscribe_logs"
	CmdPauseSync      CommandType = "pause_sync"
	CmdResumeSync     CommandType = "resume_sync"
	CmdCheckUpdate    CommandType = "check_update"
	CmdDownloadUpdate CommandType = "download_update"
	CmdShutdownAgent  CommandType = "shutdown_agent"
	// AppDB sync (Phase 5)
	CmdAppDBStatus CommandType = "appdb_status"
	CmdAppDBLogin  CommandType = "appdb_login"
	CmdAppDBLogout CommandType = "appdb_logout"
	CmdVaultPush   CommandType = "vault_push"
	CmdVaultPull   CommandType = "vault_pull"
)

// Request is a message from UI to Agent.
type Request struct {
	ID      string      `json:"id"`
	Command CommandType `json:"command"`
	Payload any         `json:"payload,omitempty"`
}

// Response is a message from Agent to UI.
type Response struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

// Event is a push message from Agent to UI (for log stream, status changes, etc.).
type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload,omitempty"`
}

// EventType for pushed events.
type EventType string

const (
	EventStatusChanged      EventType = "status:changed"
	EventLogEntry           EventType = "log:entry"
	EventSyncStarted        EventType = "sync:started"
	EventSyncCompleted      EventType = "sync:completed"
	EventSyncFailed         EventType = "sync:failed"
	EventUpdateProgress     EventType = "update:progress"
	EventCameraConnected    EventType = "camera:connected"
	EventCameraDisconnected EventType = "camera:disconnected"
	EventBackupQueued       EventType = "backup:queued"
	EventBackupCompleted    EventType = "backup:completed"
)

// Standard response codes used by Agent/UI contract.
const (
	CodeOK             = "ok"
	CodeBadRequest     = "bad_request"
	CodeTimeout        = "timeout"
	CodeUnknownCmd     = "unknown_command"
	CodeInternalError  = "internal_error"
	CodeAgentOffline   = "agent_offline"
	CodeNotImplemented = "not_implemented"
)

// --- Payload types ---

// CameraInfo describes one detected camera / card source.
type CameraInfo struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	Mode        string `json:"mode"` // "mtp" | "mass_storage"
	DriveLetter string `json:"drive_letter,omitempty"`
	Status      string `json:"status"` // "idle" | "syncing" | "error"
	LastSync    string `json:"last_sync,omitempty"`
	Progress    int    `json:"progress,omitempty"`     // files processed this pass
	ProgressMax int    `json:"progress_max,omitempty"` // media files this pass
	CurrentFile string `json:"current_file,omitempty"` // file being copied
}

// ProfileSnapshot is the resolved profile shape exposed to UI.
type ProfileSnapshot struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	BasePath      string   `json:"base_path"` // resolved (machine override applied)
	PhotoTemplate string   `json:"photo_template"`
	VideoTemplate string   `json:"video_template"`
	FileTypes     []string `json:"file_types"`
	BackupEnabled bool     `json:"backup_enabled"`
	RemoteName    string   `json:"remote_name,omitempty"`
	RemotePath    string   `json:"remote_path,omitempty"`
	Active        bool     `json:"active"`
}

// AppStatus represents the full application state snapshot.
type AppStatus struct {
	TrayColor        string       `json:"tray_color"` // green, blue, orange, red
	StatusText       string       `json:"status_text"`
	ServiceRunning   bool         `json:"service_running"`
	SyncInProgress   bool         `json:"sync_in_progress"`
	SyncPaused       bool         `json:"sync_paused"`
	ActiveProfile    string       `json:"active_profile"`
	Cameras          []CameraInfo `json:"cameras"`
	FilesSyncedTotal int          `json:"files_synced_total"`
	JobsPending      int          `json:"jobs_pending"`
	JobsFailed       int          `json:"jobs_failed"`
	LastBackup       string       `json:"last_backup,omitempty"`
	NetworkErrors    int          `json:"network_errors"`
	DetectErrors     int          `json:"detect_errors"`
	CriticalError    string       `json:"critical_error,omitempty"`
	Progress         int          `json:"progress"` // 0-100 during a job, -1 when idle
	CurrentJobName   string       `json:"current_job_name,omitempty"`
	Version          string       `json:"version"`
	MachineName      string       `json:"machine_name"`
	AppDBConnected   bool         `json:"appdb_connected"`
	UpdateAvailable  string       `json:"update_available,omitempty"`
}

// JobInfo describes a queued/running/failed background job (backup etc.).
type JobInfo struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // "upload"
	State     string `json:"state"`
	Payload   string `json:"payload"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`
}

// LogEntry represents a single log line.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARNING, ERROR, DEBUG
	Message   string    `json:"message"`
}

// StreamLogEntry is a buffered log line with monotonically increasing ID.
type StreamLogEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARN, ERROR, DEBUG
	Message   string    `json:"message"`
}

// SubscribeLogsPayload requests buffered logs after a cursor ID.
type SubscribeLogsPayload struct {
	AfterID int64  `json:"after_id,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Level   string `json:"level,omitempty"` // ALL, INFO, WARN, ERROR, DEBUG
}

// SubscribeLogsResult is the subscribe_logs IPC response payload.
type SubscribeLogsResult struct {
	Entries []StreamLogEntry `json:"entries"`
	LastID  int64            `json:"last_id"`
}

// CheckUpdateResult is the payload returned by CmdCheckUpdate.
type CheckUpdateResult struct {
	CurrentVersion     string `json:"current_version"`
	LatestVersion      string `json:"latest_version"`
	HasUpdate          bool   `json:"has_update"`
	ReleaseNotes       string `json:"release_notes,omitempty"`
	ReleaseURL         string `json:"release_url,omitempty"`
	PublishedAt        string `json:"published_at,omitempty"`
	AssetName          string `json:"asset_name,omitempty"`
	AssetURL           string `json:"asset_url,omitempty"`
	CheckedAt          string `json:"checked_at,omitempty"`
	DownloadInProgress bool   `json:"download_in_progress"`
}

// DownloadUpdatePayload requests update download.
type DownloadUpdatePayload struct {
	AssetURL  string `json:"asset_url,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
}

// DownloadUpdateResult is the payload returned by CmdDownloadUpdate.
type DownloadUpdateResult struct {
	Started            bool   `json:"started"`
	DownloadInProgress bool   `json:"download_in_progress"`
	DestinationPath    string `json:"destination_path,omitempty"`
	Message            string `json:"message,omitempty"`
}

// ScanNowPayload requests a manual scan of a camera/drive.
type ScanNowPayload struct {
	DeviceID  string   `json:"device_id,omitempty"`  // empty = all connected
	Dates     []string `json:"dates,omitempty"`      // empty = all dates
	Mode      string   `json:"mode,omitempty"`       // "copy" | "move"
	ProfileID string   `json:"profile_id,omitempty"` // empty = active profile
}

// SetProfilePayload selects the active profile.
type SetProfilePayload struct {
	ProfileID string `json:"profile_id"`
}

// BackupStatusResult reports the upload queue state.
type BackupStatusResult struct {
	StagingDir   string    `json:"staging_dir"`
	PendingFiles int       `json:"pending_files"`
	TotalSize    int64     `json:"total_size"`
	Jobs         []JobInfo `json:"jobs"`
	LastRun      string    `json:"last_run,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

// AppDBStatusResult reports cloud-sync account state.
type AppDBStatusResult struct {
	Enabled     bool   `json:"enabled"`
	LoggedIn    bool   `json:"logged_in"`
	Email       string `json:"email,omitempty"`
	DeviceLabel string `json:"device_label,omitempty"`
	LastSyncAt  string `json:"last_sync_at,omitempty"`
}

// AppDBLoginPayload carries credentials for appdb_login.
type AppDBLoginPayload struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// VaultPayload carries the passphrase for vault_push/vault_pull.
type VaultPayload struct {
	Passphrase string `json:"passphrase"`
}

// ConfigSnapshot is the Agent config returned to UI callers.
type ConfigSnapshot struct {
	// general
	ScanMode          string `json:"scan_mode"` // "once" | "continuous"
	PollInterval      int    `json:"poll_interval"`
	SyncMode          string `json:"sync_mode"` // "copy" | "move"
	OverwriteExisting bool   `json:"overwrite_existing"`
	AutoSync          bool   `json:"auto_sync"`
	StartWithWindows  bool   `json:"start_with_windows"`
	StartMinimized    bool   `json:"start_minimized"`
	MinimizeToTray    bool   `json:"minimize_to_tray"`
	// notifications
	NotifyOnConnect  bool   `json:"notify_on_connect"`
	NotifyOnComplete string `json:"notify_on_complete"` // "each" | "batch" | "off"
	// profiles
	ActiveProfile string            `json:"active_profile"`
	Profiles      []ProfileSnapshot `json:"profiles"`
	// machine-local (never synced)
	MachineName  string            `json:"machine_name"`
	ProfilePaths map[string]string `json:"profile_paths"` // profile_id -> this machine's path
	// appdb
	SyncEnabled  bool `json:"sync_enabled"`
	SyncLoggedIn bool `json:"sync_logged_in"`
}

// SaveConfigPayload is a partial config update payload.
// nil fields mean "keep existing value".
type SaveConfigPayload struct {
	ScanMode          *string `json:"scan_mode,omitempty"`
	PollInterval      *int    `json:"poll_interval,omitempty"`
	SyncMode          *string `json:"sync_mode,omitempty"`
	OverwriteExisting *bool   `json:"overwrite_existing,omitempty"`
	AutoSync          *bool   `json:"auto_sync,omitempty"`
	StartWithWindows  *bool   `json:"start_with_windows,omitempty"`
	StartMinimized    *bool   `json:"start_minimized,omitempty"`
	MinimizeToTray    *bool   `json:"minimize_to_tray,omitempty"`
	NotifyOnConnect   *bool   `json:"notify_on_connect,omitempty"`
	NotifyOnComplete  *string `json:"notify_on_complete,omitempty"`
	ActiveProfile     *string `json:"active_profile,omitempty"`
	// Full-replacement fields (UI sends the whole list/map when editing).
	Profiles       *[]Profile         `json:"profiles,omitempty"`
	ProfilePaths   *map[string]string `json:"profile_paths,omitempty"`
	FileTypePhotos *[]string          `json:"file_type_photos,omitempty"`
	FileTypeVideos *[]string          `json:"file_type_videos,omitempty"`
}

// Profile mirrors config.Profile for IPC payloads.
type Profile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	PhotoTemplate string `json:"photo_template"`
	VideoTemplate string `json:"video_template"`
	Backup        struct {
		Enabled    bool   `json:"enabled"`
		RemoteName string `json:"remote_name"`
		RemotePath string `json:"remote_path"`
		FreeSpace  bool   `json:"free_space"`
	} `json:"backup"`
}
