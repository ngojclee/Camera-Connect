// CameraConnectAgent — background daemon.
// Owns all state (cameras, sync queue, config, uploads) and serves IPC over
// a named pipe. The Wails UI is a thin client; it never touches the
// filesystem or camera APIs directly.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ngojclee/camera-connect/internal/config"
	"github.com/ngojclee/camera-connect/internal/coordinator"
	"github.com/ngojclee/camera-connect/internal/detect"
	"github.com/ngojclee/camera-connect/internal/ipc"
	"github.com/ngojclee/camera-connect/internal/logstream"
	"github.com/ngojclee/camera-connect/internal/monitor"
	winplatform "github.com/ngojclee/camera-connect/internal/platform/windows"
	"github.com/ngojclee/camera-connect/internal/store"
	"github.com/ngojclee/camera-connect/internal/syncengine"
	"github.com/ngojclee/camera-connect/internal/tray"
	updatepkg "github.com/ngojclee/camera-connect/internal/update"
)

var Version = "dev"

var downloadedUpdateVersionPattern = regexp.MustCompile(`(?i)cameraconnect.*v?(\d+(?:\.\d+){2,3}).*\.(exe|msi)$`)

func main() {
	minimized := flag.Bool("minimized", false, "Start minimized to tray")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(Version)
		return
	}

	logBuffer := logstream.NewBuffer(800)
	log.SetOutput(io.MultiWriter(os.Stderr, logstream.NewWriter(logBuffer)))

	// --- Single instance guard ---
	mutex := winplatform.NewSingleInstance("CameraConnectAgent_Mutex")
	acquired, err := mutex.TryAcquire()
	if err != nil {
		log.Fatalf("Failed to create mutex: %v", err)
	}
	if !acquired {
		log.Println("Another Agent instance is already running. Exiting.")
		os.Exit(0)
	}
	defer mutex.Release()

	// --- Load layered config (shared + machine-local) ---
	cfgDir, err := config.DefaultDir()
	if err != nil {
		log.Fatalf("Config dir error: %v", err)
	}
	cfgMgr := config.NewManager(cfgDir)
	if err := cfgMgr.Load(); err != nil {
		log.Printf("[WARN] Failed to load config, using defaults: %v", err)
	}
	if migrated, source, err := cfgMgr.MigrateFromLegacyPaths(config.LegacyPaths()); err != nil {
		log.Printf("[WARN] Legacy config migration failed: %v", err)
	} else if migrated {
		log.Printf("[INFO] Migrated legacy config from: %s", source)
	}

	exePath, exeErr := os.Executable()
	if exeErr != nil {
		log.Printf("[WARN] Unable to resolve executable path for startup registry: %v", exeErr)
	} else {
		shared := cfgMgr.Shared()
		startupMgr := winplatform.NewStartupManager()
		if err := startupMgr.SetEnabled(shared.General.StartWithWindows, exePath, shared.General.StartMinimized); err != nil {
			log.Printf("[WARN] Failed to apply startup registry setting: %v", err)
		}
	}

	// --- Open persistent store (sync history + upload retry queue) ---
	db, err := store.Open(cfgDir)
	if err != nil {
		log.Printf("[WARN] store unavailable, job retry persistence disabled: %v", err)
	} else {
		defer db.Close()
	}

	// --- Initialize core components ---
	machineName := hostNameOrUnknown()
	eventBus := coordinator.NewEventBus(64)
	appState := coordinator.NewAppState(machineName, Version)
	if p := cfgMgr.ActiveProfile(); p != nil {
		appState.SetActiveProfile(p.ID)
	}
	trayStatusPath, trayStatusErr := tray.DefaultStatusPath()
	if trayStatusErr != nil {
		log.Printf("[WARN] Tray status path unavailable: %v", trayStatusErr)
	}
	updateChecker := updatepkg.NewChecker(updatepkg.CheckerOptions{
		Repository: "ngojclee/camera-connect",
		AppName:    "CameraConnect",
	})
	if err := cleanupDownloadedUpdates(Version); err != nil {
		log.Printf("[WARN] update download cleanup failed: %v", err)
	}
	var updateMu sync.Mutex
	var cachedLatestUpdate ipc.CheckUpdateResult
	var hasCachedUpdate bool
	var updateDownloadInProgress bool

	// --- Context and goroutine lifecycle ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup

	startManaged(ctx, &wg, "event-bus", func(ctx context.Context) {
		eventBus.Run(ctx)
	})

	// --- Single-flight sync worker ---
	syncWorker := coordinator.NewSyncWorker(16, appState, eventBus)
	watchdog := coordinator.NewWatchdog(250*time.Millisecond, func(alert coordinator.WatchdogAlert) {
		log.Printf("[WARN] operation timeout op_id=%s name=%s started_at=%s deadline=%s",
			alert.OperationID, alert.OperationName, alert.StartedAt.Format(time.RFC3339), alert.DeadlineAt.Format(time.RFC3339))
		appState.SetWarning("Một thao tác đang chậm bất thường")
	})
	syncWorker.SetWatchdog(watchdog)
	startManaged(ctx, &wg, "sync-worker", func(ctx context.Context) {
		syncWorker.Run(ctx)
	})
	startManaged(ctx, &wg, "operation-watchdog", func(ctx context.Context) {
		watchdog.Run(ctx)
	})

	// --- Tray status publisher ---
	if strings.TrimSpace(trayStatusPath) != "" {
		startManaged(ctx, &wg, "tray-status-publisher", func(ctx context.Context) {
			writeTraySnapshot := func() {
				snap := appState.Snapshot()
				err := tray.WriteStatus(trayStatusPath, tray.StatusPayload{
					StatusText:     snap.StatusText,
					TrayColor:      snap.TrayColor,
					SyncInProgress: snap.SyncInProgress,
					SyncPaused:     snap.SyncPaused,
					AutoSync:       cfgMgr.Shared().General.AutoSync,
					UpdatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
				})
				if err != nil {
					log.Printf("[WARN] Failed to publish tray status snapshot: %v", err)
				}
			}
			writeTraySnapshot()
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					writeTraySnapshot()
				}
			}
		})
	}

	// --- Job queue counts → status badge ---
	if db != nil {
		startManaged(ctx, &wg, "job-counts", func(ctx context.Context) {
			refresh := func() {
				pending, failed, err := db.JobCounts(ctx)
				if err == nil {
					appState.SetJobCounts(pending, failed)
				}
			}
			refresh()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refresh()
				}
			}
		})
	}

	// --- Share/path health monitor on resolved profile base paths ---
	var pathProbe monitor.NetworkProbe
	sharePaths := profileBasePaths(cfgMgr)
	if len(sharePaths) > 0 {
		pathProbe = monitor.NewPathProbe(sharePaths)
		pollInterval := time.Duration(cfgMgr.Shared().General.PollInterval) * time.Second
		if pollInterval <= 0 {
			pollInterval = 5 * time.Second
		}
		shareHealth := monitor.NewShareHealthMonitor(monitor.ShareHealthConfig{
			CheckInterval:    pollInterval,
			ProbeTimeout:     2 * time.Second,
			FailureThreshold: 3,
			OpenTimeout:      30 * time.Second,
		}, pathProbe, monitor.ShareHealthHooks{
			OnNetworkLost: func(err error) {
				log.Printf("[WARN] destination path unstable: %v", err)
				appState.IncNetworkError()
				appState.SetWarning("Mất kết nối thư mục đích")
				eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtNetworkLost, Payload: err.Error()})
			},
			OnNetworkRecovered: func() {
				log.Printf("[INFO] destination path recovered")
				appState.RefreshDerivedStatus()
				eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtNetworkAvailable})
			},
		})
		startManaged(ctx, &wg, "path-health-monitor", func(ctx context.Context) {
			shareHealth.Run(ctx)
		})
	}

	// --- Camera detection watcher (mass storage now; MTP joins Phase 3) ---
	syncDeps := &syncengine.Deps{
		Config: cfgMgr,
		Store:  db,
		Logf:   log.Printf,
		PendingUpload: func(destPath string) {
			if db == nil {
				return
			}
			payload := fmt.Sprintf(`{"files":[%q]}`, destPath)
			if _, err := db.EnqueueJob(context.Background(), store.KindUpload, payload); err != nil {
				log.Printf("[WARN] enqueue upload job failed: %v", err)
			}
		},
	}
	deviceLoops := newDeviceLoopManager(syncWorker, syncDeps, appState, eventBus, cfgMgr)
	watcher := detect.NewWatcher(2*time.Second, detect.Hooks{
		OnConnect: func(dev detect.Device) {
			model := dev.Model
			log.Printf("[INFO] Camera connected: %s (%s mode%s)", model, dev.Mode, driveSuffix(dev))
			appState.CameraConnected(ipc.CameraInfo{
				ID:          dev.ID,
				Model:       model,
				Mode:        string(dev.Mode),
				DriveLetter: dev.DriveLetter,
				Status:      "idle",
			})
			eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtCameraStarted, Payload: dev})
			deviceLoops.Start(ctx, dev)
		},
		OnDisconnect: func(dev detect.Device) {
			log.Printf("[INFO] Camera disconnected: %s (%s)", dev.Model, dev.ID)
			deviceLoops.Stop(dev.ID)
			appState.CameraDisconnected(dev.ID)
			eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtCameraStopped, Payload: dev})
		},
		OnError: func(err error) {
			log.Printf("[WARN] detector error: %v", err)
			appState.IncDetectError()
		},
	})
	startManaged(ctx, &wg, "camera-detector", func(ctx context.Context) {
		watcher.Run(ctx)
	})

	// --- Sleep/resume detector ---
	resumeDetector := monitor.NewResumeDetector(5*time.Second, 20*time.Second, monitor.ResumeHooks{
		OnResume: func(gap time.Duration) {
			log.Printf("[INFO] resume detected after gap=%s; revalidating paths", gap.Round(time.Second))
			appState.SetWarning("Đang kiểm tra lại kết nối sau sleep/resume")
			if pathProbe == nil {
				appState.RefreshDerivedStatus()
				return
			}
			revalidateCtx, revalidateCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer revalidateCancel()
			if err := pathProbe(revalidateCtx); err != nil {
				log.Printf("[WARN] path revalidation after resume failed: %v", err)
				appState.IncNetworkError()
				appState.SetWarning("Thư mục đích chưa sẵn sàng sau resume")
				eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtNetworkLost, Payload: err.Error()})
				return
			}
			appState.RefreshDerivedStatus()
			eventBus.Emit(coordinator.InternalEvent{Type: coordinator.EvtNetworkAvailable, Payload: "resume_revalidated"})
		},
	})
	startManaged(ctx, &wg, "resume-detector", func(ctx context.Context) {
		resumeDetector.Run(ctx)
	})

	// --- IPC server (UI <-> Agent) ---
	shutdownCh := make(chan struct{}, 1)
	ipcServer := ipc.NewServer(ipc.PipeName, ipc.DefaultRequestTimeout, func(reqCtx context.Context, req ipc.Request) ipc.Response {
		return dispatchCommand(reqCtx, req, &agentDeps{
			cfgMgr:        cfgMgr,
			appState:      appState,
			logBuffer:     logBuffer,
			syncWorker:    syncWorker,
			watcher:       watcher,
			deviceLoops:   deviceLoops,
			syncDeps:      syncDeps,
			shutdownCh:    shutdownCh,
			updateChecker: updateChecker,
			updateMu:      &updateMu,
			cachedUpdate:  &cachedLatestUpdate,
			hasCached:     &hasCachedUpdate,
			downloading:   &updateDownloadInProgress,
			exePath:       exePath,
			db:            db,
		})
	})
	startManaged(ctx, &wg, "ipc-server", func(ctx context.Context) {
		if err := ipcServer.Start(ctx); err != nil {
			log.Printf("[ERROR] IPC server stopped: %v", err)
		}
	})

	// --- Tray host ---
	uiExecutable := resolveUIExecutable(exePath)
	trayManager := tray.NewManager(tray.Options{
		AppName:      "Camera Connect",
		AgentPID:     os.Getpid(),
		UIExecutable: uiExecutable,
		PipeName:     ipc.PipeName,
		StatusPath:   trayStatusPath,
	})
	if err := trayManager.Start(ctx); err != nil {
		log.Printf("[WARN] Tray bootstrap failed: %v", err)
	} else {
		log.Printf("[INFO] Tray bootstrap started (ui=%s)", uiExecutable)
	}
	defer func() {
		if err := trayManager.Stop(); err != nil {
			log.Printf("[WARN] Tray shutdown error: %v", err)
		}
	}()

	log.Printf("[INFO] CameraConnect Agent %s started (minimized=%v)", Version, *minimized)
	log.Printf("[INFO] Config dir: %s", cfgDir)
	log.Printf("[INFO] State: %s", appState.Snapshot().StatusText)

	// --- Wait for shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
		log.Println("[INFO] OS signal received, shutting down Agent...")
	case <-shutdownCh:
		log.Println("[INFO] IPC shutdown command received, shutting down Agent...")
	}

	cancel()
	_ = ipcServer.Close()
	if !waitGroupWithTimeout(&wg, 5*time.Second) {
		log.Println("[WARN] Shutdown timed out waiting for background workers.")
	}
	log.Println("[INFO] Agent stopped.")
}

// agentDeps bundles everything the IPC dispatcher needs.
type agentDeps struct {
	cfgMgr        *config.Manager
	appState      *coordinator.AppState
	logBuffer     *logstream.Buffer
	syncWorker    *coordinator.SyncWorker
	watcher       *detect.Watcher
	deviceLoops   *deviceLoopManager
	syncDeps      *syncengine.Deps
	shutdownCh    chan struct{}
	updateChecker *updatepkg.Checker
	updateMu      *sync.Mutex
	cachedUpdate  *ipc.CheckUpdateResult
	hasCached     *bool
	downloading   *bool
	exePath       string
	db            *store.Store
}

func dispatchCommand(reqCtx context.Context, req ipc.Request, d *agentDeps) ipc.Response {
	switch req.Command {
	case ipc.CmdPing:
		return ipc.Response{Success: true, Data: map[string]string{"message": "pong"}, Code: ipc.CodeOK}

	case ipc.CmdGetStatus:
		return ipc.Response{Success: true, Data: d.appState.Snapshot(), Code: ipc.CodeOK}

	case ipc.CmdGetConfig:
		return ipc.Response{Success: true, Data: configToIPCSnapshot(d.cfgMgr), Code: ipc.CodeOK}

	case ipc.CmdSaveConfig:
		payload, err := decodePayload[ipc.SaveConfigPayload](req.Payload)
		if err != nil {
			return ipc.Response{Success: false, Error: fmt.Sprintf("invalid save_config payload: %v", err), Code: ipc.CodeBadRequest}
		}
		if isEmptyConfigPatch(payload) {
			return ipc.Response{Success: false, Error: "save_config payload is empty", Code: ipc.CodeBadRequest}
		}
		if err := applyConfigPatch(d.cfgMgr, payload); err != nil {
			return ipc.Response{Success: false, Error: err.Error(), Code: ipc.CodeBadRequest}
		}
		updated := d.cfgMgr.Shared()
		if d.exePath != "" {
			startupMgr := winplatform.NewStartupManager()
			if err := startupMgr.SetEnabled(updated.General.StartWithWindows, d.exePath, updated.General.StartMinimized); err != nil {
				log.Printf("[WARN] Failed to apply startup registry after save_config: %v", err)
			}
		}
		return ipc.Response{Success: true, Data: configToIPCSnapshot(d.cfgMgr), Code: ipc.CodeOK}

	case ipc.CmdListCameras:
		return ipc.Response{Success: true, Data: d.appState.Snapshot().Cameras, Code: ipc.CodeOK}

	case ipc.CmdListProfiles:
		return ipc.Response{Success: true, Data: configToIPCSnapshot(d.cfgMgr).Profiles, Code: ipc.CodeOK}

	case ipc.CmdSetProfile:
		payload, err := decodePayload[ipc.SetProfilePayload](req.Payload)
		if err != nil || strings.TrimSpace(payload.ProfileID) == "" {
			return ipc.Response{Success: false, Error: "profile_id is required", Code: ipc.CodeBadRequest}
		}
		if d.cfgMgr.ProfileByID(payload.ProfileID) == nil {
			return ipc.Response{Success: false, Error: "profile not found: " + payload.ProfileID, Code: ipc.CodeBadRequest}
		}
		if err := d.cfgMgr.UpdateShared(func(s *config.SharedConfig) { s.ActiveProfile = payload.ProfileID }); err != nil {
			return ipc.Response{Success: false, Error: err.Error(), Code: ipc.CodeInternalError}
		}
		d.appState.SetActiveProfile(payload.ProfileID)
		log.Printf("[INFO] Active profile switched: %s", payload.ProfileID)
		return ipc.Response{Success: true, Data: map[string]string{"active_profile": payload.ProfileID}, Code: ipc.CodeOK}

	case ipc.CmdScanNow:
		payload, err := decodePayload[ipc.ScanNowPayload](req.Payload)
		if err != nil {
			return ipc.Response{Success: false, Error: fmt.Sprintf("invalid scan_now payload: %v", err), Code: ipc.CodeBadRequest}
		}
		devices := d.watcher.Devices()
		if len(devices) == 0 {
			return ipc.Response{Success: false, Error: "no camera connected", Code: ipc.CodeBadRequest}
		}
		queued := 0
		for _, dev := range devices {
			if dev.Mode != detect.ModeMassStorage {
				continue // MTP lands in Phase 3
			}
			if payload.DeviceID != "" && payload.DeviceID != dev.ID {
				continue
			}
			if err := enqueueDeviceSync(d.syncWorker, dev, d.syncDeps, d.appState, "manual_scan"); err != nil {
				log.Printf("[WARN] enqueue scan for %s failed: %v", dev.ID, err)
				continue
			}
			queued++
		}
		if queued == 0 {
			return ipc.Response{Success: false, Error: "no mass-storage camera matched (MTP support lands in Phase 3)", Code: ipc.CodeBadRequest}
		}
		return ipc.Response{Success: true, Data: map[string]string{"queued": fmt.Sprintf("%d", queued)}, Code: ipc.CodeOK}

	case ipc.CmdPauseSync:
		d.syncWorker.Pause()
		return ipc.Response{Success: true, Data: d.appState.Snapshot(), Code: ipc.CodeOK}

	case ipc.CmdResumeSync:
		d.syncWorker.Resume()
		return ipc.Response{Success: true, Data: d.appState.Snapshot(), Code: ipc.CodeOK}

	case ipc.CmdSubscribeLogs:
		payload, err := decodePayload[ipc.SubscribeLogsPayload](req.Payload)
		if err != nil {
			return ipc.Response{Success: false, Error: fmt.Sprintf("invalid subscribe_logs payload: %v", err), Code: ipc.CodeBadRequest}
		}
		if payload.Limit <= 0 {
			payload.Limit = 200
		}
		if payload.Limit > 500 {
			payload.Limit = 500
		}
		levelFilter := normalizeLogLevelFilter(payload.Level)
		scanLimit := payload.Limit
		if levelFilter != "" {
			scanLimit = 500
		}
		entries, cursor := d.logBuffer.Since(payload.AfterID, scanLimit)
		stream := make([]ipc.StreamLogEntry, 0, payload.Limit)
		for _, entry := range entries {
			if levelFilter != "" && !strings.EqualFold(entry.Level, levelFilter) {
				continue
			}
			stream = append(stream, ipc.StreamLogEntry{
				ID:        entry.ID,
				Timestamp: entry.Timestamp,
				Level:     entry.Level,
				Message:   entry.Message,
			})
			if len(stream) >= payload.Limit {
				break
			}
		}
		return ipc.Response{
			Success: true,
			Data:    ipc.SubscribeLogsResult{Entries: stream, LastID: cursor},
			Code:    ipc.CodeOK,
		}

	case ipc.CmdBackupStatus:
		result := ipc.BackupStatusResult{}
		if d.db != nil {
			jobs, err := d.db.DueJobs(reqCtx, time.Now().Add(24*time.Hour)) // show all incl. scheduled retries
			if err == nil {
				for _, j := range jobs {
					result.Jobs = append(result.Jobs, ipc.JobInfo{
						ID: j.ID, Kind: j.Kind, State: j.State, Payload: j.Payload,
						Attempts: j.Attempts, LastError: j.LastError,
					})
				}
			}
		}
		return ipc.Response{Success: true, Data: result, Code: ipc.CodeOK}

	case ipc.CmdRetryBackups:
		// Phase 4 wires the upload worker; for now just report queue depth.
		return ipc.Response{Success: true, Data: map[string]string{"status": "queued"}, Code: ipc.CodeOK}

	case ipc.CmdCheckUpdate:
		releaseInfo, err := d.updateChecker.CheckLatest(reqCtx, Version)
		if err != nil {
			return ipc.Response{Success: false, Error: fmt.Sprintf("check_update failed: %v", err), Code: ipc.CodeInternalError}
		}
		d.updateMu.Lock()
		result := toIPCUpdateResult(releaseInfo, *d.downloading)
		*d.cachedUpdate = result
		*d.hasCached = true
		d.updateMu.Unlock()
		if result.HasUpdate {
			d.appState.SetUpdateAvailable(result.LatestVersion)
		}
		return ipc.Response{Success: true, Data: result, Code: ipc.CodeOK}

	case ipc.CmdDownloadUpdate:
		payload, err := decodePayload[ipc.DownloadUpdatePayload](req.Payload)
		if err != nil {
			return ipc.Response{Success: false, Error: fmt.Sprintf("invalid download_update payload: %v", err), Code: ipc.CodeBadRequest}
		}
		d.updateMu.Lock()
		if *d.downloading {
			d.updateMu.Unlock()
			return ipc.Response{Success: false, Error: "update download is already in progress", Code: ipc.CodeBadRequest}
		}
		assetURL := strings.TrimSpace(payload.AssetURL)
		assetName := strings.TrimSpace(payload.AssetName)
		if assetURL == "" && *d.hasCached {
			assetURL = strings.TrimSpace(d.cachedUpdate.AssetURL)
			if assetName == "" {
				assetName = strings.TrimSpace(d.cachedUpdate.AssetName)
			}
		}
		if assetURL == "" {
			d.updateMu.Unlock()
			return ipc.Response{Success: false, Error: "asset_url is required (run check_update first or pass payload)", Code: ipc.CodeBadRequest}
		}
		*d.downloading = true
		d.updateMu.Unlock()

		updateDir, err := defaultUpdateDownloadDir()
		if err != nil {
			d.updateMu.Lock()
			*d.downloading = false
			d.updateMu.Unlock()
			return ipc.Response{Success: false, Error: fmt.Sprintf("resolve update directory: %v", err), Code: ipc.CodeInternalError}
		}
		resolvedName := updatepkg.ResolveAssetName(assetURL, assetName)
		destination := filepath.Join(updateDir, resolvedName)

		go func(url, dest string) {
			d.appState.SetWarning("Đang tải bản cập nhật...")
			log.Printf("[INFO] update download started asset=%s destination=%s", url, dest)
			downloadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			lastPercent := -1
			err := d.updateChecker.DownloadToFile(downloadCtx, url, dest, func(downloaded, total int64) {
				if total <= 0 {
					return
				}
				percent := int(float64(downloaded) * 100 / float64(total))
				if percent != lastPercent && percent%10 == 0 {
					lastPercent = percent
					log.Printf("[INFO] update download progress=%d%%", percent)
				}
			})
			d.updateMu.Lock()
			*d.downloading = false
			if *d.hasCached {
				d.cachedUpdate.DownloadInProgress = false
			}
			d.updateMu.Unlock()
			if err != nil {
				log.Printf("[ERROR] update download failed: %v", err)
				d.appState.SetWarning("Tải cập nhật thất bại")
			} else if launchErr := launchDownloadedUpdate(dest); launchErr != nil {
				log.Printf("[WARN] update downloaded but launch failed: %v", launchErr)
				d.appState.SetWarning("Đã tải bản cập nhật. Hãy mở file cài đặt thủ công.")
			} else {
				log.Printf("[INFO] update installer launched: %s", dest)
				d.appState.SetWarning("Đã tải và mở bộ cài cập nhật")
			}
			go func() {
				time.Sleep(4 * time.Second)
				d.appState.RefreshDerivedStatus()
			}()
		}(assetURL, destination)

		d.updateMu.Lock()
		if *d.hasCached {
			d.cachedUpdate.DownloadInProgress = true
		}
		d.updateMu.Unlock()
		return ipc.Response{
			Success: true,
			Data:    ipc.DownloadUpdateResult{Started: true, DownloadInProgress: true, DestinationPath: destination, Message: "update download started"},
			Code:    ipc.CodeOK,
		}

	case ipc.CmdAppDBStatus:
		return ipc.Response{Success: true, Data: ipc.AppDBStatusResult{Enabled: false, LoggedIn: false}, Code: ipc.CodeOK}

	case ipc.CmdShutdownAgent:
		log.Println("[INFO] Shutdown requested via IPC from UI")
		select {
		case d.shutdownCh <- struct{}{}:
		default:
		}
		return ipc.Response{Success: true, Data: map[string]string{"message": "shutdown initiated"}, Code: ipc.CodeOK}

	default:
		return ipc.Response{Success: false, Error: fmt.Sprintf("unsupported command: %s", req.Command), Code: ipc.CodeUnknownCmd}
	}
}

// ---------- helpers ----------

func startManaged(ctx context.Context, wg *sync.WaitGroup, name string, run func(context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		run(ctx)
		log.Printf("[DEBUG] worker stopped: %s", name)
	}()
}

func waitGroupWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// ---------- device sync loops ----------

// enqueueDeviceSync queues one sync pass over a device on the single-flight worker.
func enqueueDeviceSync(worker *coordinator.SyncWorker, dev detect.Device, deps *syncengine.Deps, state *coordinator.AppState, trigger string) error {
	jobName := fmt.Sprintf("sync_%s", strings.ReplaceAll(dev.ID, ":", "_"))
	return worker.Enqueue(coordinator.SyncJob{
		Name:           jobName,
		OperationID:    fmt.Sprintf("%s_%s_%d", jobName, trigger, time.Now().UTC().UnixNano()),
		MaxRunDuration: 60 * time.Minute, // large video batches need room
		Execute: func(ctx context.Context) error {
			state.SetCameraStatus(dev.ID, "syncing")
			defer state.SetCameraStatus(dev.ID, "idle")
			res, err := syncengine.SyncDevice(ctx, dev, deps)
			if err != nil {
				return err
			}
			if res.Downloaded > 0 {
				state.AddFilesSynced(res.Downloaded)
			}
			state.SetCameraLastSync(dev.ID, time.Now().Format(time.RFC3339))
			return nil
		},
	})
}

// deviceLoopManager runs per-device sync loops: once on connect when
// auto_sync is on, then repeating at poll_interval in continuous mode.
type deviceLoopManager struct {
	mu     sync.Mutex
	loops  map[string]context.CancelFunc
	worker *coordinator.SyncWorker
	deps   *syncengine.Deps
	state  *coordinator.AppState
	bus    *coordinator.EventBus
	cfgMgr *config.Manager
}

func newDeviceLoopManager(worker *coordinator.SyncWorker, deps *syncengine.Deps, state *coordinator.AppState, bus *coordinator.EventBus, cfgMgr *config.Manager) *deviceLoopManager {
	return &deviceLoopManager{loops: map[string]context.CancelFunc{}, worker: worker, deps: deps, state: state, bus: bus, cfgMgr: cfgMgr}
}

// Start begins the sync loop for a connected device.
func (m *deviceLoopManager) Start(parent context.Context, dev detect.Device) {
	m.Stop(dev.ID)
	if dev.Mode != detect.ModeMassStorage {
		return // MTP engines land in Phase 3
	}
	shared := m.cfgMgr.Shared()
	if !shared.General.AutoSync {
		return // manual mode: wait for scan_now
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.loops[dev.ID] = cancel
	m.mu.Unlock()

	go func() {
		_ = enqueueDeviceSync(m.worker, dev, m.deps, m.state, "auto_connect")
		if !strings.EqualFold(shared.General.ScanMode, "continuous") {
			return // "once" mode: single pass per connect
		}
		poll := time.Duration(shared.General.PollInterval) * time.Second
		if poll <= 0 {
			poll = 3 * time.Second
		}
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = enqueueDeviceSync(m.worker, dev, m.deps, m.state, "auto_poll")
			}
		}
	}()
}

// Stop cancels a device's sync loop (e.g., on disconnect).
func (m *deviceLoopManager) Stop(deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.loops[deviceID]; ok {
		cancel()
		delete(m.loops, deviceID)
	}
}

func driveSuffix(dev detect.Device) string {
	if dev.DriveLetter != "" {
		return ", " + dev.DriveLetter
	}
	return ""
}

func hostNameOrUnknown() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "UNKNOWN"
	}
	return host
}

// profileBasePaths returns all resolved profile paths to health-check.
func profileBasePaths(cfgMgr *config.Manager) []string {
	shared := cfgMgr.Shared()
	out := make([]string, 0, len(shared.Profiles))
	for _, p := range shared.Profiles {
		if path := cfgMgr.ResolvedBasePath(p.ID); strings.TrimSpace(path) != "" {
			out = append(out, path)
		}
	}
	return out
}

func configToIPCSnapshot(m *config.Manager) ipc.ConfigSnapshot {
	shared := m.Shared()
	local := m.Local()
	profiles := make([]ipc.ProfileSnapshot, 0, len(shared.Profiles))
	for _, p := range shared.Profiles {
		profiles = append(profiles, ipc.ProfileSnapshot{
			ID:            p.ID,
			Name:          p.Name,
			BasePath:      m.ResolvedBasePath(p.ID),
			PhotoTemplate: p.PhotoTemplate,
			VideoTemplate: p.VideoTemplate,
			FileTypes:     p.FileTypes,
			BackupEnabled: p.Backup.Enabled,
			RemoteName:    p.Backup.RemoteName,
			RemotePath:    p.Backup.RemotePath,
			Active:        p.ID == shared.ActiveProfile,
		})
	}
	return ipc.ConfigSnapshot{
		ScanMode:          shared.General.ScanMode,
		PollInterval:      shared.General.PollInterval,
		SyncMode:          shared.General.SyncMode,
		OverwriteExisting: shared.General.OverwriteExisting,
		AutoSync:          shared.General.AutoSync,
		StartWithWindows:  shared.General.StartWithWindows,
		StartMinimized:    shared.General.StartMinimized,
		MinimizeToTray:    shared.General.MinimizeToTray,
		NotifyOnConnect:   shared.Notifications.OnConnect,
		NotifyOnComplete:  shared.Notifications.OnComplete,
		ActiveProfile:     shared.ActiveProfile,
		Profiles:          profiles,
		MachineName:       local.MachineName,
		ProfilePaths:      local.ProfilePaths,
		SyncEnabled:       shared.Sync.Enabled,
	}
}

func applyConfigPatch(m *config.Manager, patch ipc.SaveConfigPayload) error {
	if patch.PollInterval != nil && *patch.PollInterval <= 0 {
		return errors.New("poll_interval must be > 0")
	}
	if patch.ScanMode != nil && *patch.ScanMode != "once" && *patch.ScanMode != "continuous" {
		return errors.New("scan_mode must be 'once' or 'continuous'")
	}
	if patch.SyncMode != nil && *patch.SyncMode != "copy" && *patch.SyncMode != "move" {
		return errors.New("sync_mode must be 'copy' or 'move'")
	}
	if patch.NotifyOnComplete != nil {
		switch *patch.NotifyOnComplete {
		case "each", "batch", "off":
		default:
			return errors.New("notify_on_complete must be 'each', 'batch', or 'off'")
		}
	}
	return m.UpdateShared(func(s *config.SharedConfig) {
		if patch.ScanMode != nil {
			s.General.ScanMode = *patch.ScanMode
		}
		if patch.PollInterval != nil {
			s.General.PollInterval = *patch.PollInterval
		}
		if patch.SyncMode != nil {
			s.General.SyncMode = *patch.SyncMode
		}
		if patch.OverwriteExisting != nil {
			s.General.OverwriteExisting = *patch.OverwriteExisting
		}
		if patch.AutoSync != nil {
			s.General.AutoSync = *patch.AutoSync
		}
		if patch.StartWithWindows != nil {
			s.General.StartWithWindows = *patch.StartWithWindows
		}
		if patch.StartMinimized != nil {
			s.General.StartMinimized = *patch.StartMinimized
		}
		if patch.MinimizeToTray != nil {
			s.General.MinimizeToTray = *patch.MinimizeToTray
		}
		if patch.NotifyOnConnect != nil {
			s.Notifications.OnConnect = *patch.NotifyOnConnect
		}
		if patch.NotifyOnComplete != nil {
			s.Notifications.OnComplete = *patch.NotifyOnComplete
		}
		if patch.ActiveProfile != nil {
			s.ActiveProfile = strings.TrimSpace(*patch.ActiveProfile)
		}
	})
}

func isEmptyConfigPatch(p ipc.SaveConfigPayload) bool {
	return p.ScanMode == nil && p.PollInterval == nil && p.SyncMode == nil &&
		p.OverwriteExisting == nil && p.AutoSync == nil &&
		p.StartWithWindows == nil && p.StartMinimized == nil && p.MinimizeToTray == nil &&
		p.NotifyOnConnect == nil && p.NotifyOnComplete == nil && p.ActiveProfile == nil
}

func decodePayload[T any](raw any) (T, error) {
	var out T
	if raw == nil {
		return out, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return out, err
	}
	if len(data) == 0 || string(data) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func resolveUIExecutable(agentExePath string) string {
	if strings.TrimSpace(agentExePath) == "" {
		return ""
	}
	dir := filepath.Dir(agentExePath)
	candidates := []string{
		filepath.Join(dir, "CameraConnect.exe"),
		filepath.Join(dir, "CameraConnectUI.exe"),
		filepath.Join(dir, "ui.exe"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func normalizeLogLevelFilter(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "ALL":
		return ""
	case "INFO", "WARN", "ERROR", "DEBUG":
		return strings.ToUpper(strings.TrimSpace(raw))
	case "WARNING":
		return "WARN"
	default:
		return ""
	}
}

func defaultUpdateDownloadDir() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("LOCALAPPDATA environment variable is not set")
	}
	return filepath.Join(localAppData, "CameraConnect", "updates"), nil
}

func launchDownloadedUpdate(path string) error {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
	if ext != ".exe" && ext != ".msi" {
		return fmt.Errorf("downloaded asset is not directly launchable: %s", path)
	}
	cmd := exec.Command("cmd", "/c", "start", "", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	return cmd.Start()
}

func cleanupDownloadedUpdates(currentVersion string) error {
	updateDir, err := defaultUpdateDownloadDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(updateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		fullPath := filepath.Join(updateDir, name)
		lowerName := strings.ToLower(name)
		if strings.HasSuffix(lowerName, ".part") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			if now.Sub(info.ModTime().UTC()) >= 30*time.Minute {
				if removeErr := os.Remove(fullPath); removeErr == nil {
					log.Printf("[INFO] removed stale partial update download: %s", fullPath)
				}
			}
			continue
		}
		version := extractedDownloadedUpdateVersion(name)
		if version == "" {
			continue
		}
		cmp, cmpErr := updatepkg.CompareVersions(version, currentVersion)
		if cmpErr != nil || cmp > 0 {
			continue
		}
		if removeErr := os.Remove(fullPath); removeErr == nil {
			log.Printf("[INFO] removed stale downloaded update: %s", fullPath)
		}
	}
	return nil
}

func extractedDownloadedUpdateVersion(name string) string {
	match := downloadedUpdateVersionPattern.FindStringSubmatch(strings.TrimSpace(name))
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func toIPCUpdateResult(release updatepkg.LatestRelease, downloadInProgress bool) ipc.CheckUpdateResult {
	notes := strings.TrimSpace(release.ReleaseNotes)
	if len(notes) > 6000 {
		notes = notes[:6000] + "\n...(truncated)"
	}
	return ipc.CheckUpdateResult{
		CurrentVersion:     strings.TrimSpace(release.CurrentVersion),
		LatestVersion:      strings.TrimSpace(release.LatestVersion),
		HasUpdate:          release.HasUpdate,
		ReleaseNotes:       notes,
		ReleaseURL:         strings.TrimSpace(release.ReleaseURL),
		PublishedAt:        strings.TrimSpace(release.PublishedAt),
		AssetName:          strings.TrimSpace(release.AssetName),
		AssetURL:           strings.TrimSpace(release.AssetURL),
		CheckedAt:          strings.TrimSpace(release.CheckedAt),
		DownloadInProgress: downloadInProgress,
	}
}
