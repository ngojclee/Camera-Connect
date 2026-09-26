package coordinator

import (
	"sort"
	"sync"

	"github.com/ngojclee/camera-connect/internal/ipc"
)

// AppState is the single authoritative state cache for the Agent.
// Both tray and IPC reads come from here — no direct filesystem queries from UI.
type AppState struct {
	mu sync.RWMutex

	serviceRunning  bool
	cameras         map[string]ipc.CameraInfo
	syncInProgress  bool
	syncPaused      bool
	activeProfile   string
	trayColor       string // green, blue, orange, red
	statusText      string
	lastBackup      string
	filesSynced     int
	jobsPending     int
	jobsFailed      int
	networkErrors   int
	detectErrors    int
	criticalError   string
	progress        int // 0-100 during a job, -1 when idle
	currentJobName  string
	version         string
	machineName     string
	appdbConnected  bool
	updateAvailable string
}

func NewAppState(machineName, version string) *AppState {
	return &AppState{
		trayColor:   "green",
		statusText:  "Ready",
		cameras:     map[string]ipc.CameraInfo{},
		progress:    -1,
		machineName: machineName,
		version:     version,
	}
}

// Snapshot returns a copy of the current state for IPC responses.
func (s *AppState) Snapshot() ipc.AppStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cams := make([]ipc.CameraInfo, 0, len(s.cameras))
	for _, c := range s.cameras {
		cams = append(cams, c)
	}
	sort.Slice(cams, func(i, j int) bool { return cams[i].ID < cams[j].ID })

	return ipc.AppStatus{
		TrayColor:        s.trayColor,
		StatusText:       s.statusText,
		ServiceRunning:   s.serviceRunning,
		SyncInProgress:   s.syncInProgress,
		SyncPaused:       s.syncPaused,
		ActiveProfile:    s.activeProfile,
		Cameras:          cams,
		FilesSyncedTotal: s.filesSynced,
		JobsPending:      s.jobsPending,
		JobsFailed:       s.jobsFailed,
		LastBackup:       s.lastBackup,
		NetworkErrors:    s.networkErrors,
		DetectErrors:     s.detectErrors,
		CriticalError:    s.criticalError,
		Progress:         s.progress,
		CurrentJobName:   s.currentJobName,
		Version:          s.version,
		MachineName:      s.machineName,
		AppDBConnected:   s.appdbConnected,
		UpdateAvailable:  s.updateAvailable,
	}
}

func (s *AppState) SetServiceRunning(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serviceRunning = v
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) CameraConnected(info ipc.CameraInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info.Status == "" {
		info.Status = "idle"
	}
	s.cameras[info.ID] = info
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) CameraDisconnected(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cameras, id)
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) SetCameraStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cameras[id]; ok {
		c.Status = status
		if status != "syncing" {
			c.Progress = 0
			c.ProgressMax = 0
			c.CurrentFile = ""
		}
		s.cameras[id] = c
	}
}

// SetCameraProgress reports per-file progress for a syncing camera.
// current=0 resets; callers pass i before processing each file.
func (s *AppState) SetCameraProgress(id string, current, max int, file string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cameras[id]; ok {
		c.Progress = current
		c.ProgressMax = max
		c.CurrentFile = file
		s.cameras[id] = c
	}
}

func (s *AppState) SetCameraLastSync(id, ts string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cameras[id]; ok {
		c.LastSync = ts
		s.cameras[id] = c
	}
}

func (s *AppState) SetSyncing(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncInProgress = v
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) SetSyncPaused(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncPaused = v
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) SetActiveProfile(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeProfile = id
}

func (s *AppState) SetWarning(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trayColor = "orange"
	s.statusText = text
}

func (s *AppState) SetError(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.criticalError = text
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) ClearError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.criticalError = ""
	s.recomputeDerivedStatusLocked()
}

func (s *AppState) SetLastBackup(info string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBackup = info
}

// LastBackup returns the last successful backup timestamp string.
func (s *AppState) LastBackup() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBackup
}

func (s *AppState) AddFilesSynced(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesSynced += n
}

func (s *AppState) SetJobCounts(pending, failed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobsPending = pending
	s.jobsFailed = failed
}

func (s *AppState) SetAppDBConnected(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appdbConnected = v
}

func (s *AppState) SetUpdateAvailable(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateAvailable = version
}

func (s *AppState) SetProgress(progress int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = progress
}

func (s *AppState) SetCurrentJobName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentJobName = name
}

func (s *AppState) IncNetworkError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkErrors++
}

func (s *AppState) IncDetectError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detectErrors++
}

// RefreshDerivedStatus clears temporary warning state and restores status from current flags.
func (s *AppState) RefreshDerivedStatus() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputeDerivedStatusLocked()
}

// TrayColor returns the current tray icon color.
func (s *AppState) TrayColor() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trayColor
}

func (s *AppState) recomputeDerivedStatusLocked() {
	switch {
	case s.criticalError != "":
		s.trayColor = "red"
		s.statusText = s.criticalError
	case s.syncInProgress:
		s.trayColor = "red"
		if s.currentJobName != "" {
			s.statusText = "Syncing: " + s.currentJobName
		} else {
			s.statusText = "Syncing..."
		}
	case s.syncPaused:
		s.trayColor = "orange"
		s.statusText = "Sync Paused"
	case len(s.cameras) > 0:
		s.trayColor = "blue"
		s.statusText = "Camera connected"
	case s.serviceRunning:
		s.trayColor = "green"
		s.statusText = "Watching for cameras"
	default:
		s.trayColor = "green"
		s.statusText = "Ready"
	}
}
