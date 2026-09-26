// Package config implements the layered configuration model:
//
//	config.yaml        — SHARED settings (synced across machines via AppDB user scope)
//	config.local.yaml  — MACHINE settings (never synced; per-host overrides)
//
// Resolution order for a profile's effective base_path:
//
//	local.ProfilePaths[id] → profile.BasePath → shared default → builtin default
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------- Schema ----------

// GeneralConfig holds sync-loop behavior settings (shared).
type GeneralConfig struct {
	ScanMode          string `yaml:"scan_mode"`     // "once" | "continuous"
	PollInterval      int    `yaml:"poll_interval"` // seconds (continuous mode)
	SyncMode          string `yaml:"sync_mode"`     // "copy" | "move" (delete from camera)
	OverwriteExisting bool   `yaml:"overwrite_existing"`
	AutoSync          bool   `yaml:"auto_sync"` // start sync when camera connects
	StartWithWindows  bool   `yaml:"start_with_windows"`
	StartMinimized    bool   `yaml:"start_minimized"`
	MinimizeToTray    bool   `yaml:"minimize_to_tray"`
}

// NotifyConfig holds notification toggles (shared).
type NotifyConfig struct {
	OnConnect  bool   `yaml:"on_connect"`  // notify when a camera is detected
	OnComplete string `yaml:"on_complete"` // "each" | "batch" | "off"
}

// FileTypeConfig lists allowed extensions per media kind (shared defaults).
type FileTypeConfig struct {
	Photos []string `yaml:"photos"`
	Videos []string `yaml:"videos"`
}

// SyncConfig controls optional AppDB cloud sync (shared).
type SyncConfig struct {
	Enabled bool `yaml:"enabled"` // AppDB login + settings sync
}

// Profile is one destination preset the user can switch between.
type Profile struct {
	ID            string       `yaml:"id"`
	Name          string       `yaml:"name"`
	BasePath      string       `yaml:"base_path"`
	PhotoTemplate string       `yaml:"photo_template"`
	VideoTemplate string       `yaml:"video_template"`
	FileTypes     []string     `yaml:"file_types,omitempty"` // empty = use FileTypeConfig defaults
	Backup        BackupConfig `yaml:"backup"`
}

// BackupConfig controls cloud upload for a profile (shared shape;
// remote credentials live in secrets\rclone.conf, never here).
type BackupConfig struct {
	Enabled    bool   `yaml:"enabled"`
	RemoteName string `yaml:"remote_name"` // rclone remote, e.g. "gdrive"
	RemotePath string `yaml:"remote_path"` // e.g. "Backup/Photos"
	FreeSpace  bool   `yaml:"free_space"`  // delete local after VERIFIED upload
}

// SharedConfig is config.yaml — synced across machines when AppDB is on.
type SharedConfig struct {
	General       GeneralConfig  `yaml:"general"`
	Notifications NotifyConfig   `yaml:"notifications"`
	FileTypes     FileTypeConfig `yaml:"file_types"`
	Sync          SyncConfig     `yaml:"sync"`
	ActiveProfile string         `yaml:"active_profile"`
	Profiles      []Profile      `yaml:"profiles"`
}

// LocalConfig is config.local.yaml — this machine only, never synced.
type LocalConfig struct {
	MachineName  string            `yaml:"machine_name"`
	ProfilePaths map[string]string `yaml:"profile_paths"` // profile_id → base path on THIS machine
	RclonePath   string            `yaml:"rclone_path"`   // override rclone.exe location
	Window       WindowConfig      `yaml:"window"`
}

// WindowConfig stores last window geometry (machine-local).
type WindowConfig struct {
	Width  int `yaml:"width,omitempty"`
	Height int `yaml:"height,omitempty"`
	X      int `yaml:"x,omitempty"`
	Y      int `yaml:"y,omitempty"`
}

// ---------- Defaults ----------

func defaultShared() SharedConfig {
	return SharedConfig{
		General: GeneralConfig{
			ScanMode:          "once",
			PollInterval:      3,
			SyncMode:          "copy",
			OverwriteExisting: true,
			AutoSync:          false,
			StartWithWindows:  false,
			StartMinimized:    true,
			MinimizeToTray:    true,
		},
		Notifications: NotifyConfig{OnConnect: true, OnComplete: "batch"},
		FileTypes: FileTypeConfig{
			Photos: []string{"ARW", "CR2", "CR3", "NEF", "NRW", "RAF", "ORF", "RW2", "DNG", "GPR", "JPG", "JPEG", "HEIF", "HIF"},
			Videos: []string{"MP4", "MTS", "AVCHD", "MOV", "LRV", "MKV"},
		},
		Profiles:      []Profile{},
		ActiveProfile: "",
	}
}

func defaultLocal() LocalConfig {
	host, _ := os.Hostname()
	return LocalConfig{
		MachineName:  host,
		ProfilePaths: map[string]string{},
	}
}

// ---------- Manager ----------

// Manager loads/saves the layered config with thread safety.
type Manager struct {
	mu     sync.RWMutex
	dir    string
	shared SharedConfig
	local  LocalConfig
}

// DefaultDir returns %LOCALAPPDATA%\CameraConnect.
func DefaultDir() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("LOCALAPPDATA environment variable not set")
	}
	return filepath.Join(localAppData, "CameraConnect"), nil
}

func (m *Manager) sharedPath() string { return filepath.Join(m.dir, "config.yaml") }
func (m *Manager) localPath() string  { return filepath.Join(m.dir, "config.local.yaml") }

// Dir returns the config directory (also holds cache.db, logs\, secrets\).
func (m *Manager) Dir() string { return m.dir }

// SecretsDir returns %LOCALAPPDATA%\CameraConnect\secrets.
func (m *Manager) SecretsDir() string { return filepath.Join(m.dir, "secrets") }

// NewManager creates a manager bound to a config directory.
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, shared: defaultShared(), local: defaultLocal()}
}

// Load reads both config files, merging over defaults. Missing files are OK.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shared = defaultShared()
	m.local = defaultLocal()

	if data, err := os.ReadFile(m.sharedPath()); err == nil {
		if err := yaml.Unmarshal(data, &m.shared); err != nil {
			return fmt.Errorf("parse config.yaml: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read config.yaml: %w", err)
	}

	if data, err := os.ReadFile(m.localPath()); err == nil {
		if err := yaml.Unmarshal(data, &m.local); err != nil {
			return fmt.Errorf("parse config.local.yaml: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read config.local.yaml: %w", err)
	}

	m.applyDefaults()
	return nil
}

// Save writes both files atomically-ish (separate writes; each file small).
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	sharedData, err := yaml.Marshal(&m.shared)
	if err != nil {
		return fmt.Errorf("marshal shared config: %w", err)
	}
	sharedHeader := "# Camera Connect — SHARED config (synced via AppDB when enabled)\n\n"
	if err := os.WriteFile(m.sharedPath(), []byte(sharedHeader+string(sharedData)), 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	localData, err := yaml.Marshal(&m.local)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	localHeader := "# Camera Connect — MACHINE config (never synced)\n\n"
	if err := os.WriteFile(m.localPath(), []byte(localHeader+string(localData)), 0o644); err != nil {
		return fmt.Errorf("write config.local.yaml: %w", err)
	}
	return nil
}

func (m *Manager) applyDefaults() {
	s := &m.shared
	if s.General.PollInterval <= 0 {
		s.General.PollInterval = 3
	}
	if s.General.ScanMode == "" {
		s.General.ScanMode = "once"
	}
	if s.General.SyncMode == "" {
		s.General.SyncMode = "copy"
	}
	if s.Notifications.OnComplete == "" {
		s.Notifications.OnComplete = "batch"
	}
	if len(s.FileTypes.Photos) == 0 {
		s.FileTypes.Photos = defaultShared().FileTypes.Photos
	}
	if len(s.FileTypes.Videos) == 0 {
		s.FileTypes.Videos = defaultShared().FileTypes.Videos
	}
	if m.local.ProfilePaths == nil {
		m.local.ProfilePaths = map[string]string{}
	}
	if m.local.MachineName == "" {
		m.local.MachineName = defaultLocal().MachineName
	}
}

// ---------- Accessors ----------

// Shared returns a copy of the shared config.
func (m *Manager) Shared() SharedConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shared
}

// Local returns a copy of the machine-local config.
func (m *Manager) Local() LocalConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lc := m.local
	paths := make(map[string]string, len(lc.ProfilePaths))
	for k, v := range lc.ProfilePaths {
		paths[k] = v
	}
	lc.ProfilePaths = paths
	return lc
}

// UpdateShared replaces the shared config and persists both files.
func (m *Manager) UpdateShared(fn func(*SharedConfig)) error {
	m.mu.Lock()
	fn(&m.shared)
	m.applyDefaults()
	m.mu.Unlock()
	return m.Save()
}

// UpdateLocal replaces parts of the local config and persists.
func (m *Manager) UpdateLocal(fn func(*LocalConfig)) error {
	m.mu.Lock()
	fn(&m.local)
	m.applyDefaults()
	m.mu.Unlock()
	return m.Save()
}

// ActiveProfile resolves the currently active profile, or nil if none.
func (m *Manager) ActiveProfile() *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.shared.Profiles {
		if m.shared.Profiles[i].ID == m.shared.ActiveProfile {
			p := m.shared.Profiles[i]
			return &p
		}
	}
	if len(m.shared.Profiles) > 0 {
		p := m.shared.Profiles[0]
		return &p
	}
	return nil
}

// ProfileByID finds a profile by id (nil if missing).
func (m *Manager) ProfileByID(id string) *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.shared.Profiles {
		if m.shared.Profiles[i].ID == id {
			p := m.shared.Profiles[i]
			return &p
		}
	}
	return nil
}

// ResolvedBasePath returns the effective base path for a profile on this
// machine — machine override wins over the profile's shared base_path.
func (m *Manager) ResolvedBasePath(profileID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.local.ProfilePaths[profileID]; ok && strings.TrimSpace(p) != "" {
		return p
	}
	for i := range m.shared.Profiles {
		if m.shared.Profiles[i].ID == profileID {
			return m.shared.Profiles[i].BasePath
		}
	}
	return ""
}

// SetProfilePathOverride sets (or clears with "") this machine's path for a profile.
func (m *Manager) SetProfilePathOverride(profileID, path string) error {
	return m.UpdateLocal(func(l *LocalConfig) {
		if strings.TrimSpace(path) == "" {
			delete(l.ProfilePaths, profileID)
		} else {
			l.ProfilePaths[profileID] = path
		}
	})
}

// IsFileTypeAllowed checks an extension against profile override or defaults.
func (m *Manager) IsFileTypeAllowed(profileID, filename string) bool {
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext == "" {
		return false
	}
	allowed := m.fileTypesFor(profileID)
	for _, e := range allowed {
		if ext == strings.ToUpper(e) {
			return true
		}
	}
	return false
}

func (m *Manager) fileTypesFor(profileID string) []string {
	if p := m.ProfileByID(profileID); p != nil && len(p.FileTypes) > 0 {
		return p.FileTypes
	}
	s := m.Shared()
	out := make([]string, 0, len(s.FileTypes.Photos)+len(s.FileTypes.Videos))
	out = append(out, s.FileTypes.Photos...)
	out = append(out, s.FileTypes.Videos...)
	return out
}

// ---------- Legacy migration (Python {exe}Config.yaml) ----------

// LegacyPaths returns candidate paths of the old Python config.
// Ordered most → least likely.
func LegacyPaths() []string {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		base := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
		paths = append(paths,
			filepath.Join(exeDir, base+"Config.yaml"),
			filepath.Join(exeDir, "CameraConnectConfig.yaml"),
		)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		paths = append(paths,
			filepath.Join(local, "Camera Connect", "CameraConnectConfig.yaml"),
			filepath.Join(local, "Sony Camera", "SonyCameraConfig.yaml"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".sony_camera_sync", "config.yaml"))
	}
	return uniquePaths(paths)
}

// legacyPythonConfig mirrors the Python YAML shape for migration.
type legacyPythonConfig struct {
	Cameras []struct {
		Name     string `yaml:"name"`
		Protocol string `yaml:"protocol"`
		USBPID   string `yaml:"usb_pid"`
	} `yaml:"cameras"`
	Destination struct {
		BasePath      string `yaml:"base_path"`
		PhotoTemplate string `yaml:"photo_template"`
		VideoTemplate string `yaml:"video_template"`
	} `yaml:"destination"`
	FileTypes FileTypeConfig `yaml:"file_types"`
	General   struct {
		ScanMode          string `yaml:"scan_mode"`
		PollInterval      int    `yaml:"poll_interval"`
		SyncMode          string `yaml:"sync_mode"`
		OverwriteExisting bool   `yaml:"overwrite_existing"`
		StartWithWindows  bool   `yaml:"start_with_windows"`
		MinimizeToTray    bool   `yaml:"minimize_to_tray"`
		StartupAutoSync   bool   `yaml:"startup_auto_sync"`
	} `yaml:"general"`
	Backup struct {
		Enabled    bool   `yaml:"enabled"`
		RemoteName string `yaml:"remote_name"`
		RemotePath string `yaml:"remote_path"`
		FreeSpace  bool   `yaml:"delete_local_after_upload"`
	} `yaml:"backup"`
	Notifications struct {
		OnConnect  bool   `yaml:"on_camera_connect"`
		OnComplete string `yaml:"on_copy_complete"`
	} `yaml:"notifications"`
}

// MigrateFromLegacyPaths imports the first existing Python config,
// mapping it onto a "default" profile + shared settings. The legacy file
// is backed up inside the config dir. Returns (migrated, sourcePath, err).
func (m *Manager) MigrateFromLegacyPaths(legacyPaths []string) (bool, string, error) {
	m.mu.RLock()
	targetShared := m.sharedPath()
	m.mu.RUnlock()

	if _, err := os.Stat(targetShared); err == nil {
		return false, "", nil // already migrated
	} else if !os.IsNotExist(err) {
		return false, "", fmt.Errorf("stat config: %w", err)
	}

	for _, candidate := range legacyPaths {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if samePath(clean, targetShared) {
			continue
		}
		data, err := os.ReadFile(clean)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, "", fmt.Errorf("read legacy config %s: %w", clean, err)
		}

		var legacy legacyPythonConfig
		if err := yaml.Unmarshal(data, &legacy); err != nil {
			return false, "", fmt.Errorf("parse legacy config %s: %w", clean, err)
		}

		shared := defaultShared()
		shared.General.ScanMode = legacy.General.ScanMode
		if legacy.General.PollInterval > 0 {
			shared.General.PollInterval = legacy.General.PollInterval
		}
		shared.General.SyncMode = legacy.General.SyncMode
		shared.General.OverwriteExisting = legacy.General.OverwriteExisting
		shared.General.StartWithWindows = legacy.General.StartWithWindows
		shared.General.MinimizeToTray = legacy.General.MinimizeToTray
		shared.General.AutoSync = legacy.General.StartupAutoSync
		shared.Notifications.OnConnect = legacy.Notifications.OnConnect
		if legacy.Notifications.OnComplete != "" {
			shared.Notifications.OnComplete = legacy.Notifications.OnComplete
		}
		if len(legacy.FileTypes.Photos) > 0 {
			shared.FileTypes.Photos = legacy.FileTypes.Photos
		}
		if len(legacy.FileTypes.Videos) > 0 {
			shared.FileTypes.Videos = legacy.FileTypes.Videos
		}

		// Old destination becomes the default profile
		if strings.TrimSpace(legacy.Destination.BasePath) != "" {
			shared.Profiles = []Profile{{
				ID:            "default",
				Name:          "Default",
				BasePath:      legacy.Destination.BasePath,
				PhotoTemplate: valueOr(legacy.Destination.PhotoTemplate, "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"),
				VideoTemplate: valueOr(legacy.Destination.VideoTemplate, "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"),
				Backup: BackupConfig{
					Enabled:    legacy.Backup.Enabled,
					RemoteName: valueOr(legacy.Backup.RemoteName, "gdrive"),
					RemotePath: valueOr(legacy.Backup.RemotePath, "CameraBackup"),
					FreeSpace:  false, // force OFF on migrate — verified-delete ships in pipeline v2
				},
			}}
			shared.ActiveProfile = "default"
		}

		m.mu.Lock()
		m.shared = shared
		m.applyDefaults()
		m.mu.Unlock()

		if err := m.Save(); err != nil {
			return false, "", fmt.Errorf("save migrated config: %w", err)
		}
		if err := m.backupLegacyFile(clean); err != nil {
			return false, "", err
		}
		return true, clean, nil
	}

	return false, "", nil
}

func (m *Manager) backupLegacyFile(sourcePath string) error {
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read legacy file for backup: %w", err)
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("create config dir for backup: %w", err)
	}
	stamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(m.dir, fmt.Sprintf("legacy_backup_%s_%s", stamp, filepath.Base(sourcePath)))
	if err := os.WriteFile(backupPath, sourceData, 0o644); err != nil {
		return fmt.Errorf("write legacy backup %s: %w", backupPath, err)
	}
	return nil
}

func valueOr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func uniquePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}
