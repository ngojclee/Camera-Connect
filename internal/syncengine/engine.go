package syncengine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngojclee/camera-connect/internal/backup"
	"github.com/ngojclee/camera-connect/internal/camera/massstorage"
	"github.com/ngojclee/camera-connect/internal/config"
	"github.com/ngojclee/camera-connect/internal/detect"
	"github.com/ngojclee/camera-connect/internal/store"
)

// Result summarizes one sync pass over a device.
type Result struct {
	DeviceID   string
	Camera     string
	Downloaded int
	Skipped    int
	Failed     int
	DeletedSrc int
	DestPaths  []string // successfully written files (for upload staging)
	ProfileID  string
}

// Deps are the engine's collaborators.
type Deps struct {
	Config *config.Manager
	Store  *store.Store // may be nil → history not recorded
	Logf   func(format string, args ...any)
	// PendingUpload receives each staged file for the upload queue.
	// destRel is the final relative path inside the profile base path;
	// stagedPath is where the verified copy currently lives.
	PendingUpload func(profile *config.Profile, destRel, stagedPath string)
	// Progress is called after each file decision (copied or skipped) —
	// current counts files processed so far, total is the media count.
	Progress func(current, total int, fileName string)
}

func (d *Deps) logf(format string, args ...any) {
	if d != nil && d.Logf != nil {
		d.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// MediaSource abstracts a camera backend (mass storage today, MTP in Phase 3).
type MediaSource interface {
	Connect() error
	ListMedia(ctx context.Context, scanRoot string) ([]massstorage.MediaFile, error)
	CopyTo(ctx context.Context, f massstorage.MediaFile, destPath string, overwrite bool) (int64, bool, error)
	Delete(f massstorage.MediaFile) error
	SupportsDelete() bool
}

// ScanOptions carries per-job overrides for a sync pass.
type ScanOptions struct {
	ProfileID string // "" = shared active profile (manual scans may override)
	Mode      string // "" = shared General.SyncMode; "copy" | "move"
}

// SyncDevice runs one full pass over a detected device.
func SyncDevice(ctx context.Context, dev detect.Device, deps *Deps) (*Result, error) {
	return SyncDeviceOpts(ctx, dev, deps, ScanOptions{})
}

// SyncDeviceProfile runs one pass bound to a specific profile; an empty
// profileID falls back to the shared active profile.
func SyncDeviceProfile(ctx context.Context, dev detect.Device, deps *Deps, profileID string) (*Result, error) {
	return SyncDeviceOpts(ctx, dev, deps, ScanOptions{ProfileID: profileID})
}

// SyncDeviceOpts is the full entry point: profileID overrides the active
// profile for this pass only (never mutates config), Mode overrides the
// shared copy/move behavior for this pass only.
func SyncDeviceOpts(ctx context.Context, dev detect.Device, deps *Deps, opts ScanOptions) (*Result, error) {
	res := &Result{DeviceID: dev.ID, Camera: dev.Model}

	var src MediaSource
	scanRoot := "DCIM"
	switch dev.Mode {
	case detect.ModeMassStorage:
		src = massstorage.NewHandler(dev.DriveLetter, dev.Model)
	case detect.ModeMTP:
		if dev.DriverError != 0 {
			return res, fmt.Errorf("MTP driver error (Code %d, status %s) — Windows cannot expose %s to Shell. Fix: uninstall the device in Device Manager and re-plug, then retry",
				dev.DriverError, dev.DriverStatus, dev.Model)
		}
		mtpSrc, err := openMTPSource(dev)
		if err != nil {
			return res, err
		}
		src = mtpSrc
		scanRoot = "" // MTP walks storages itself
	default:
		return res, fmt.Errorf("unknown device mode %q", dev.Mode)
	}

	var profile *config.Profile
	if opts.ProfileID != "" {
		profile = deps.Config.ProfileByID(opts.ProfileID)
		if profile == nil {
			return res, fmt.Errorf("profile not found: %s", opts.ProfileID)
		}
	} else {
		profile = deps.Config.ActiveProfile()
	}
	if profile == nil {
		return res, fmt.Errorf("no active profile configured")
	}
	res.ProfileID = profile.ID
	basePath := deps.Config.ResolvedBasePath(profile.ID)
	if strings.TrimSpace(basePath) == "" {
		return res, fmt.Errorf("profile %q has no base_path on this machine", profile.ID)
	}

	if err := src.Connect(); err != nil {
		return res, err
	}

	files, err := src.ListMedia(ctx, scanRoot)
	if err != nil {
		return res, fmt.Errorf("list media on %s: %w", dev.ID, err)
	}

	valid := make([]massstorage.MediaFile, 0, len(files))
	for _, f := range files {
		if deps.Config.IsFileTypeAllowed(profile.ID, f.Name) {
			valid = append(valid, f)
		}
	}
	if len(valid) == 0 {
		deps.logf("[INFO] No new media on %s (%s)", dev.DriveLetter, dev.Model)
		return res, nil
	}

	shared := deps.Config.Shared()
	overwrite := shared.General.OverwriteExisting
	mode := shared.General.SyncMode
	if opts.Mode != "" {
		mode = opts.Mode
	}
	moveMode := strings.EqualFold(mode, "move")

	// Route: backup-enabled profiles stage into <base>/_staging/<id>/ so
	// rclone only ever sees the scoped batch; otherwise copy to final dest.
	staging := ""
	if profile.Backup.Enabled && strings.TrimSpace(profile.Backup.RemoteName) != "" {
		staging = backup.StagingDir(basePath, profile.ID)
	}

	skippedByDate := map[string]int{}
	total := len(valid)
	for i, f := range valid {
		if deps.Progress != nil {
			deps.Progress(i, total, f.Name)
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		destRel := destRelFor(profile, dev.Model, f)
		finalDest := filepath.Join(basePath, filepath.FromSlash(destRel))
		writeTo := finalDest
		if staging != "" {
			writeTo = filepath.Join(staging, filepath.FromSlash(destRel))
		}
		if _, err := os.Stat(finalDest); err == nil && !overwrite {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, destRel)
			continue
		}
		// Exists + overwrite: same-size file is still skipped (idempotent).
		// MTP often reports Size=0 — can't compare, so existence alone wins
		// (camera file names are unique; Python version used the same rule).
		if info, err := os.Stat(finalDest); err == nil && overwrite && (f.Size <= 0 || info.Size() == f.Size) {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, destRel)
			continue
		}
		// A lingering staged copy from a crashed upload = already downloaded;
		// let the upload worker handle it (don't re-copy from camera).
		if staging != "" {
			if info, err := os.Stat(writeTo); err == nil && (f.Size <= 0 || info.Size() == f.Size) {
				res.Skipped++
				recordFile(ctx, deps, f, dev.Model, profile.ID, destRel)
				continue
			}
		}

		written, already, err := src.CopyTo(ctx, f, writeTo, overwrite)
		if err != nil {
			res.Failed++
			deps.logf("[ERROR] copy failed %s: %v", f.Name, err)
			continue
		}
		if already {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, destRel)
			continue
		}

		res.Downloaded++
		res.DestPaths = append(res.DestPaths, writeTo)
		recordFile(ctx, deps, f, dev.Model, profile.ID, destRel)
		if staging != "" && deps.PendingUpload != nil {
			deps.PendingUpload(profile, destRel, writeTo)
		}
		deps.logf("[INFO] Synced %s (%d bytes) → %s", f.Name, written, writeTo)

		if moveMode && src.SupportsDelete() {
			if err := src.Delete(f); err != nil {
				deps.logf("[WARN] move mode: delete failed %s: %v", f.Name, err)
			} else {
				res.DeletedSrc++
			}
		} else if moveMode && !src.SupportsDelete() {
			deps.logf("[WARN] move mode requested but %s cannot delete (copy only)", dev.Mode)
		}
	}

	if skipped := len(skippedByDate); skipped > 0 {
		deps.logf("[INFO] Skipped %d existing files on %s", res.Skipped, dev.DriveLetter)
	}
	deps.logf("[INFO] Sync %s done: %d new, %d skipped, %d failed", dev.Model, res.Downloaded, res.Skipped, res.Failed)
	return res, nil
}

// destRelFor resolves the final relative path <rendered template>/<filename>
// inside the profile base path (also used as the path inside _staging/).
func destRelFor(profile *config.Profile, camera string, f massstorage.MediaFile) string {
	tmpl := profile.PhotoTemplate
	fileType := "photo"
	if f.IsVideo {
		fileType = "video"
		if strings.TrimSpace(profile.VideoTemplate) != "" {
			tmpl = profile.VideoTemplate
		}
	}
	dt := f.DateModified
	if dt.IsZero() {
		dt = time.Now()
	}
	folder := RenderTemplate(tmpl, camera, fileType, dt)
	return filepath.Join(folder, f.Name)
}

func recordFile(ctx context.Context, deps *Deps, f massstorage.MediaFile, camera, profileID, dest string) {
	if deps.Store == nil {
		return
	}
	if err := deps.Store.RecordFile(ctx, f.RelPath, camera, profileID, f.Size, dest); err != nil {
		deps.logf("[WARN] history record failed for %s: %v", f.Name, err)
	}
}
