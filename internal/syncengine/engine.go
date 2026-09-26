package syncengine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	// PendingUpload receives each verified dest path for the upload queue.
	PendingUpload func(destPath string)
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

// SyncDevice runs one full pass over a detected device.
func SyncDevice(ctx context.Context, dev detect.Device, deps *Deps) (*Result, error) {
	res := &Result{DeviceID: dev.ID, Camera: dev.Model}

	var src MediaSource
	scanRoot := "DCIM"
	switch dev.Mode {
	case detect.ModeMassStorage:
		src = massstorage.NewHandler(dev.DriveLetter, dev.Model)
	case detect.ModeMTP:
		mtpSrc, err := openMTPSource(dev)
		if err != nil {
			return res, err
		}
		src = mtpSrc
		scanRoot = "" // MTP walks storages itself
	default:
		return res, fmt.Errorf("unknown device mode %q", dev.Mode)
	}

	profile := deps.Config.ActiveProfile()
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
	moveMode := strings.EqualFold(shared.General.SyncMode, "move")

	skippedByDate := map[string]int{}
	for _, f := range valid {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		dest := destPathFor(basePath, profile, dev.Model, f)
		if _, err := os.Stat(dest); err == nil && !overwrite {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, dest)
			continue
		}
		// Exists + overwrite: same-size file is still skipped (idempotent).
		if info, err := os.Stat(dest); err == nil && overwrite && info.Size() == f.Size {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, dest)
			continue
		}

		written, already, err := src.CopyTo(ctx, f, dest, overwrite)
		if err != nil {
			res.Failed++
			deps.logf("[ERROR] copy failed %s: %v", f.Name, err)
			continue
		}
		if already {
			res.Skipped++
			skippedByDate[f.DateModified.Format("2006-01-02")]++
			recordFile(ctx, deps, f, dev.Model, profile.ID, dest)
			continue
		}

		res.Downloaded++
		res.DestPaths = append(res.DestPaths, dest)
		recordFile(ctx, deps, f, dev.Model, profile.ID, dest)
		if deps.PendingUpload != nil {
			deps.PendingUpload(dest)
		}
		deps.logf("[INFO] Synced %s (%d bytes) → %s", f.Name, written, dest)

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

// destPathFor resolves <base>/<rendered template>/<filename>.
func destPathFor(basePath string, profile *config.Profile, camera string, f massstorage.MediaFile) string {
	tmpl := profile.PhotoTemplate
	fileType := "photo"
	if f.IsVideo {
		fileType = "video"
		if strings.TrimSpace(profile.VideoTemplate) != "" {
			tmpl = profile.VideoTemplate
		}
	}
	folder := RenderTemplate(tmpl, camera, fileType, f.DateModified)
	return filepath.Join(basePath, folder, f.Name)
}

func recordFile(ctx context.Context, deps *Deps, f massstorage.MediaFile, camera, profileID, dest string) {
	if deps.Store == nil {
		return
	}
	if err := deps.Store.RecordFile(ctx, f.RelPath, camera, profileID, f.Size, dest); err != nil {
		deps.logf("[WARN] history record failed for %s: %v", f.Name, err)
	}
}
