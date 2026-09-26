// Package massstorage accesses cameras mounted as removable drives.
// Ported from src/camera/mass_storage_handler.py.
package massstorage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MediaFile is one file discovered on the card.
type MediaFile struct {
	Name         string
	Size         int64
	DateModified time.Time
	RelPath      string // path relative to drive root, e.g. "DCIM/100MSDCF/DSC0001.ARW"
	IsVideo      bool
}

// Handler performs filesystem operations on one mounted camera drive.
type Handler struct {
	DriveLetter string // "E:\\"
	Model       string
}

// NewHandler creates a handler for a drive letter.
func NewHandler(driveLetter, model string) *Handler {
	return &Handler{DriveLetter: driveLetter, Model: model}
}

// Connect verifies the drive exists and is readable.
func (h *Handler) Connect() error {
	info, err := os.Stat(h.DriveLetter)
	if err != nil {
		return fmt.Errorf("drive %s not accessible: %w", h.DriveLetter, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", h.DriveLetter)
	}
	if _, err := os.ReadDir(h.DriveLetter); err != nil {
		return fmt.Errorf("cannot read %s: %w", h.DriveLetter, err)
	}
	return nil
}

// videoExtSet marks files treated as videos (for template choice + sidecar cleanup).
var videoExtSet = map[string]bool{
	"MP4": true, "MTS": true, "AVCHD": true, "MOV": true, "LRV": true,
	"MKV": true, "MPG": true, "M2TS": true,
}

var dateFolderRe = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)

// ListMedia recursively scans camera media folders. If scanRoot is "DCIM",
// Sony's PRIVATE/M4ROOT/CLIP is also scanned (AVCHD/MTS videos live there).
func (h *Handler) ListMedia(ctx context.Context, scanRoot string) ([]MediaFile, error) {
	var files []MediaFile

	root := filepath.Join(h.DriveLetter, scanRoot)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		if err := h.scanFolder(ctx, root, filepath.ToSlash(scanRoot), &files); err != nil {
			return files, err
		}
	}

	if strings.EqualFold(scanRoot, "DCIM") {
		privRoot := filepath.Join(h.DriveLetter, "PRIVATE", "M4ROOT", "CLIP")
		if info, err := os.Stat(privRoot); err == nil && info.IsDir() {
			if err := h.scanFolder(ctx, privRoot, "PRIVATE/M4ROOT/CLIP", &files); err != nil {
				return files, err
			}
		}
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].DateModified.Equal(files[j].DateModified) {
			return files[i].RelPath < files[j].RelPath
		}
		return files[i].DateModified.Before(files[j].DateModified)
	})
	return files, nil
}

func (h *Handler) scanFolder(ctx context.Context, dir, relDir string, out *[]MediaFile) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("scan %s: %w", relDir, err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := relDir + "/" + e.Name()
		upper := strings.ToUpper(rel)
		// Skip thumbnail folders (Sony THMBNL, Canon/misc THUMBNAIL)
		if strings.Contains(upper, "THMBNL") || strings.Contains(upper, "THUMBNAIL") || strings.Contains(upper, ".THMBNAILS") {
			continue
		}
		if e.IsDir() {
			if err := h.scanFolder(ctx, filepath.Join(dir, e.Name()), rel, out); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		date := info.ModTime()
		// Prefer date encoded in folder name (Sony: DCIM/2025-12-26/) — camera
		// mtime is unreliable across filesystems.
		if m := dateFolderRe.FindStringSubmatch(relDir); m != nil {
			if t, err := time.ParseInLocation("2006-01-02", m[0], time.Local); err == nil {
				date = t
			}
		}
		ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(e.Name()), "."))
		*out = append(*out, MediaFile{
			Name:         e.Name(),
			Size:         info.Size(),
			DateModified: date,
			RelPath:      rel,
			IsVideo:      videoExtSet[ext],
		})
	}
	return nil
}

// CopyTo copies a media file to dest via a ".part" temp file, then verifies
// the copied size before rename — an interrupted copy can never masquerade
// as complete. Returns (bytesWritten, alreadyExisted, error).
func (h *Handler) CopyTo(ctx context.Context, f MediaFile, destPath string, overwrite bool) (int64, bool, error) {
	srcPath := filepath.Join(h.DriveLetter, filepath.FromSlash(f.RelPath))
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, false, fmt.Errorf("open source %s: %w", srcPath, err)
	}
	defer src.Close()

	if info, err := os.Stat(destPath); err == nil {
		if !overwrite {
			return info.Size(), true, nil
		}
		if info.Size() == f.Size {
			return info.Size(), true, nil // identical size → treat as already synced
		}
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, false, fmt.Errorf("mkdir %s: %w", filepath.Dir(destPath), err)
	}

	tmpPath := destPath + ".part"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return 0, false, fmt.Errorf("create %s: %w", tmpPath, err)
	}

	written, copyErr := io.Copy(tmp, src)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return written, false, fmt.Errorf("copy %s: %w", f.Name, copyErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return written, false, fmt.Errorf("sync %s: %w", tmpPath, syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return written, false, fmt.Errorf("close %s: %w", tmpPath, closeErr)
	}

	// Verify size before committing the rename.
	if f.Size > 0 && written != f.Size {
		_ = os.Remove(tmpPath)
		return written, false, fmt.Errorf("size mismatch for %s: wrote %d, expected %d", f.Name, written, f.Size)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return written, false, fmt.Errorf("rename to %s: %w", destPath, err)
	}
	return written, false, nil
}

// Delete removes the file from the card plus Sony sidecars
// (thumbnails in PRIVATE/M4ROOT/THMBNL, XML metadata next to the clip).
func (h *Handler) Delete(f MediaFile) error {
	srcPath := filepath.Join(h.DriveLetter, filepath.FromSlash(f.RelPath))
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("file not found for deletion: %s", srcPath)
	}

	if f.IsVideo {
		h.deleteVideoSidecars(srcPath)
	}
	if err := os.Remove(srcPath); err != nil {
		return fmt.Errorf("delete %s: %w", srcPath, err)
	}
	return nil
}

// deleteVideoSidecars removes associated THMBNL thumbs and M01 XML files.
func (h *Handler) deleteVideoSidecars(srcPath string) {
	stem := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))

	// Thumbnails: THMBNL/<stem>*.{JPG,THM}
	thmDir := filepath.Join(h.DriveLetter, "PRIVATE", "M4ROOT", "THMBNL")
	if entries, err := os.ReadDir(thmDir); err == nil {
		for _, e := range entries {
			name := strings.ToUpper(e.Name())
			if strings.HasPrefix(name, strings.ToUpper(stem)) &&
				(strings.HasSuffix(name, ".JPG") || strings.HasSuffix(name, ".THM")) {
				_ = os.Remove(filepath.Join(thmDir, e.Name()))
			}
		}
	}

	// XML sidecars: same dir as clip, <stem>*.XML
	dir := filepath.Dir(srcPath)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(strings.ToUpper(name), strings.ToUpper(stem)) &&
				strings.HasSuffix(strings.ToUpper(name), ".XML") {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}
