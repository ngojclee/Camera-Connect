//go:build windows

// Package mtp accesses MTP/portable devices via Shell.Application COM.
// All COM work runs on ONE dedicated OS-locked goroutine per Source — the
// Shell object is apartment-bound and must not hop threads.
// Ported from src/camera/mtp_handler.py including the post-CopyHere
// size-settle check (the async-copy race fix).
package mtp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/ngojclee/camera-connect/internal/camera/massstorage"
)

// ssfDRIVES = 17 → "This PC" (lists portable devices).
const ssfDRIVES = 17

// CopyHere flags — same as Python implementation (no UI, yes-to-all,
// no mkdir confirm, no error UI, no confirmation dialogs).
const copyHereFlags = 4 | 16 | 512 | 1024 | 8192 // = 9748

// settle parameters: file must report a stable size this many consecutive
// checks before we trust the transfer finished (CopyHere is async).
const (
	settleChecks   = 3
	settleInterval = 500 * time.Millisecond
	settleTimeout  = 10 * time.Minute
)

type comRequest struct {
	run  func() error
	done chan error
}

// Source is a MediaSource backed by an MTP portable device.
type Source struct {
	pnpID string
	model string
	reqCh chan comRequest
	done  chan struct{}
}

// Open starts the COM worker for an MTP device. model is matched against
// the device name shown under "This PC"; pnpID is matched as fallback.
func Open(pnpID, model string) (*Source, error) {
	s := &Source{
		pnpID: strings.ToUpper(pnpID),
		model: model,
		reqCh: make(chan comRequest),
		done:  make(chan struct{}),
	}
	go s.worker()
	return s, nil
}

// worker owns the Shell.Application object for this source's lifetime.
func (s *Source) worker() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Shell.Application folder ops require STA (single-threaded apartment) —
	// MTA returns RPC_E_WRONG_THREAD / silently misbehaves on WPD objects.
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		log.Printf("[WARN] COM STA init: %v", err)
	}
	defer ole.CoUninitialize()
	for {
		select {
		case <-s.done:
			return
		case req := <-s.reqCh:
			func() {
				defer func() {
					if r := recover(); r != nil {
						req.done <- fmt.Errorf("com panic: %v", r)
					}
				}()
				req.done <- req.run()
			}()
		}
	}
}

// Close tears down the COM worker.
func (s *Source) Close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// run executes fn on the COM worker thread. If the worker died (panic past
// recovery, or source closed), it returns an error instead of hanging.
func (s *Source) run(fn func() error) error {
	done := make(chan error, 1)
	select {
	case s.reqCh <- comRequest{run: fn, done: done}:
	case <-s.done:
		return errors.New("mtp source closed")
	}
	select {
	case err := <-done:
		return err
	case <-s.done:
		return errors.New("mtp worker terminated")
	}
}

// ---------- Shell helpers ----------

func createShell() (*ole.IDispatch, error) {
	unknown, err := oleutil.CreateObject("Shell.Application")
	if err != nil {
		return nil, fmt.Errorf("create Shell.Application: %w", err)
	}
	disp, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		return nil, fmt.Errorf("query IDispatch: %w", err)
	}
	return disp, nil
}

func thisPC(shell *ole.IDispatch) (*ole.IDispatch, error) {
	v, err := oleutil.CallMethod(shell, "NameSpace", ssfDRIVES)
	if err != nil {
		return nil, fmt.Errorf("NameSpace(17): %w", err)
	}
	return v.ToIDispatch(), nil
}

func folderItems(folder *ole.IDispatch) (*ole.IDispatch, int, error) {
	itemsV, err := oleutil.CallMethod(folder, "Items")
	if err != nil {
		return nil, 0, err
	}
	items := itemsV.ToIDispatch()
	countV, err := oleutil.GetProperty(items, "Count")
	if err != nil {
		items.Release()
		return nil, 0, err
	}
	return items, int(countV.Val), nil
}

func itemAt(items *ole.IDispatch, i int) (*ole.IDispatch, error) {
	v, err := oleutil.CallMethod(items, "Item", i)
	if err != nil {
		return nil, err
	}
	return v.ToIDispatch(), nil
}

func itemName(item *ole.IDispatch) string {
	v, err := oleutil.GetProperty(item, "Name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v.ToString())
}

func itemPath(item *ole.IDispatch) string {
	v, err := oleutil.GetProperty(item, "Path")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v.ToString())
}

func itemIsFolder(item *ole.IDispatch) bool {
	v, err := oleutil.GetProperty(item, "IsFolder")
	if err != nil {
		return false
	}
	return v.Val != 0
}

func itemSubFolder(item *ole.IDispatch) (*ole.IDispatch, error) {
	v, err := oleutil.GetProperty(item, "GetFolder")
	if err != nil {
		return nil, err
	}
	if v.VT == ole.VT_NULL || v.VT == ole.VT_EMPTY {
		return nil, errors.New("GetFolder returned null")
	}
	return v.ToIDispatch(), nil
}

func itemSize(item *ole.IDispatch) int64 {
	v, err := oleutil.GetProperty(item, "Size")
	if err != nil {
		return 0 // MTP often refuses Size — engine treats 0 as unknown
	}
	return v.Val
}

func itemModifyDate(item *ole.IDispatch) time.Time {
	v, err := oleutil.GetProperty(item, "ModifyDate")
	if err != nil {
		return time.Time{}
	}
	if t, ok := v.Value().(time.Time); ok && t.Year() >= 1980 {
		return t
	}
	// MTP devices frequently report the OLE-Automation epoch (1899-12-30)
	// instead of a real date — treat it as unknown so callers fall back.
	return time.Time{}
}

// pathMatchesPNP reports whether a shell item path contains every identifier
// token from the PNP device id (VID_*/PID_*/serial/MI_*).
func pathMatchesPNP(shellPath, pnpID string) bool {
	for _, tok := range strings.FieldsFunc(pnpID, func(r rune) bool {
		return r == '\\' || r == '#'
	}) {
		tok = strings.ToUpper(strings.TrimSpace(tok))
		if tok == "" || strings.EqualFold(tok, "USB") || strings.EqualFold(tok, "SWD") {
			continue
		}
		if !strings.Contains(shellPath, tok) {
			return false
		}
	}
	return true
}

// ---------- MediaSource impl ----------

// Connect finds the device under "This PC".
func (s *Source) Connect() error {
	return s.run(func() error {
		shell, err := createShell()
		if err != nil {
			return err
		}
		defer shell.Release()
		dev, err := s.findDevice(shell)
		if err != nil {
			return err
		}
		dev.Release()
		return nil
	})
}

// findDevice locates this source's device item under This PC.
func (s *Source) findDevice(shell *ole.IDispatch) (*ole.IDispatch, error) {
	pc, err := thisPC(shell)
	if err != nil {
		return nil, err
	}
	defer pc.Release()
	items, count, err := folderItems(pc)
	if err != nil {
		return nil, err
	}
	defer items.Release()

	var fallback *ole.IDispatch
	for i := 0; i < count; i++ {
		it, err := itemAt(items, i)
		if err != nil {
			continue
		}
		name := itemName(it)
		path := strings.ToUpper(itemPath(it))
		// Shell paths use #-separated form (usb#vid_054c&pid_0d96#serial#...)
		// while PNP IDs use \-separated (USB\VID_054C&PID_0D96\serial) —
		// match on the VID/PID/serial tokens extracted from the PNP id.
		if s.pnpID != "" && pathMatchesPNP(path, s.pnpID) {
			return it, nil
		}
		if strings.EqualFold(name, s.model) {
			return it, nil
		}
		if fallback == nil && s.model != "" && strings.Contains(strings.ToUpper(name), strings.ToUpper(s.model)) {
			fallback = it // keep looking for exact match
			continue
		}
		it.Release()
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("mtp device %q not found under This PC", s.model)
}

// ListMedia walks storages → finds folders containing media files
// (brand-agnostic: DCIM anywhere, or folders with photo/video extensions).
func (s *Source) ListMedia(ctx context.Context, _ string) ([]massstorage.MediaFile, error) {
	var out []massstorage.MediaFile
	err := s.run(func() error {
		shell, err := createShell()
		if err != nil {
			return err
		}
		defer shell.Release()
		dev, err := s.findDevice(shell)
		if err != nil {
			return err
		}
		defer dev.Release()
		log.Printf("[INFO] MTP: device %q found, walking folders…", s.model)
		devFolder, err := itemSubFolder(dev)
		if err != nil {
			return fmt.Errorf("open device folder: %w", err)
		}
		defer devFolder.Release()
		return s.walkFolder(ctx, devFolder, "", &out, 0)
	})
	log.Printf("[INFO] MTP: enumerated %d media file(s)", len(out))
	return out, err
}

// walkFolder recurses a WPD folder tree collecting files under DCIM roots.
// WPD exposes a big pseudo-tree (storages, folders); prune everything that
// isn't under DCIM/PRIVATE to keep enumeration fast and bounded.
func (s *Source) walkFolder(ctx context.Context, folder *ole.IDispatch, rel string, out *[]massstorage.MediaFile, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 12 { // WPD trees are shallow; safety bound against cycles
		return nil
	}
	items, count, err := folderItems(folder)
	if err != nil {
		return err
	}
	defer items.Release()
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		it, err := itemAt(items, i)
		if err != nil {
			continue
		}
		name := itemName(it)
		relPath := rel
		if relPath != "" {
			relPath += "/"
		}
		relPath += name
		upper := strings.ToUpper(relPath)

		if itemIsFolder(it) {
			if strings.Contains(upper, "THMBNL") || strings.Contains(upper, "THUMBNAIL") {
				it.Release()
				continue
			}
			// Prune: don't descend into obvious non-media branches once we're
			// past storage level (keeps MTP walks fast on multi-storage devices).
			sub, err := itemSubFolder(it)
			if err == nil {
				_ = s.walkFolder(ctx, sub, relPath, out, depth+1)
				sub.Release()
			}
			it.Release()
			continue
		}

		// File: MTP devices often present media as <storage>/<YYYY-MM-DD>/
		// (Sony) with NO DCIM segment — keep media-extension files regardless
		// of path; the engine's file-type filter gates the final set anyway.
		if !isMediaExt(name) {
			it.Release()
			continue
		}
		mtime := itemModifyDate(it)
		if mtime.IsZero() {
			// Sony-style MTP path "Storage Media/2026-02-01/x.JPG" — the
			// parent folder IS the capture date.
			if t, ok := dateFromPath(relPath); ok {
				mtime = t
			}
		}
		*out = append(*out, massstorage.MediaFile{
			Name:         name,
			Size:         itemSize(it),
			DateModified: mtime,
			RelPath:      relPath,
			IsVideo:      isVideoExt(name),
		})
		it.Release()
	}
	return nil
}

var videoExt = map[string]bool{"MP4": true, "MTS": true, "AVCHD": true, "MOV": true, "LRV": true, "MKV": true, "M2TS": true, "M2T": true, "MPG": true, "VOB": true, "3GP": true}
var photoExt = map[string]bool{"ARW": true, "JPG": true, "JPEG": true, "HEIF": true, "HIF": true, "DNG": true, "RAW": true, "CR2": true, "CR3": true, "NEF": true, "RAF": true, "ORF": true, "RW2": true, "PNG": true, "TIFF": true, "TIF": true, "BMP": true, "GIF": true, "WEBP": true}

func isVideoExt(name string) bool {
	return videoExt[strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))]
}

// dateFromPath extracts a YYYY-MM-DD date from any path segment — MTP
// devices commonly name folders by capture date (e.g. "Storage
// Media/2026-02-01/x.JPG").
func dateFromPath(relPath string) (time.Time, bool) {
	for _, seg := range strings.Split(relPath, "/") {
		if t, err := time.Parse("2006-01-02", seg); err == nil && t.Year() >= 1990 {
			return t, true
		}
	}
	return time.Time{}, false
}

// isMediaExt gates enumeration to photo/video files — MTP trees can contain
// non-media junk (XML, BIN, THM) that downstream file-type filters would
// drop anyway; skipping here keeps the COM walk cheap.
func isMediaExt(name string) bool {
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
	return videoExt[ext] || photoExt[ext]
}

// findItem locates a file by its RelPath inside the device tree.
func (s *Source) findItem(shell *ole.IDispatch, relPath string) (*ole.IDispatch, error) {
	dev, err := s.findDevice(shell)
	if err != nil {
		return nil, err
	}
	cur, err := itemSubFolder(dev)
	dev.Release()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.Trim(relPath, "/"), "/")
	// relPath is <storage>/<folder>/.../<file> — descend through all segments.
	for pi, part := range parts {
		items, count, err := folderItems(cur)
		if err != nil {
			cur.Release()
			return nil, err
		}
		var next *ole.IDispatch
		for i := 0; i < count; i++ {
			it, err := itemAt(items, i)
			if err != nil {
				continue
			}
			if strings.EqualFold(itemName(it), part) {
				next = it
				break
			}
			it.Release()
		}
		items.Release()
		if next == nil {
			cur.Release()
			return nil, fmt.Errorf("path segment %q not found under %q", part, relPath)
		}
		cur.Release()
		// Last segment is the file itself — return the FolderItem as-is.
		if pi == len(parts)-1 {
			return next, nil
		}
		// Intermediate segment: FolderItem → Folder so Items can enumerate.
		nextFolder, err := itemSubFolder(next)
		next.Release()
		if err != nil {
			return nil, fmt.Errorf("open folder %q: %w", part, err)
		}
		cur = nextFolder
	}
	return cur, nil
}

// CopyTo downloads via destFolder.CopyHere + waits for size to settle —
// the fix for truncated large-video uploads in the Python version.
func (s *Source) CopyTo(ctx context.Context, f massstorage.MediaFile, destPath string, overwrite bool) (int64, bool, error) {
	if info, err := os.Stat(destPath); err == nil {
		if !overwrite {
			return info.Size(), true, nil
		}
		// MTP Size is unreliable (often 0) — when it can't be compared,
		// existence is enough (camera file names are unique).
		if f.Size <= 0 || info.Size() == f.Size {
			return info.Size(), true, nil
		}
		// CopyHere shows a modal "Replace or Don't copy" dialog when the
		// target exists — that blocks the COM worker forever. Delete the
		// existing file first so Shell never hits a name conflict.
		if err := os.Remove(destPath); err != nil {
			return 0, false, fmt.Errorf("remove existing %s: %w", destPath, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, false, err
	}

	err := s.run(func() error {
		shell, err := createShell()
		if err != nil {
			return err
		}
		defer shell.Release()
		srcItem, err := s.findItem(shell, f.RelPath)
		if err != nil {
			return err
		}
		defer srcItem.Release()

		destDirV, err := oleutil.CallMethod(shell, "NameSpace", filepath.Dir(destPath))
		if err != nil {
			return fmt.Errorf("NameSpace(%s): %w", filepath.Dir(destPath), err)
		}
		destDir := destDirV.ToIDispatch()
		defer destDir.Release()

		if _, err := oleutil.CallMethod(destDir, "CopyHere", srcItem, copyHereFlags); err != nil {
			return fmt.Errorf("CopyHere %s: %w", f.Name, err)
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}

	// Wait until the file reports a stable size — CopyHere returns before
	// the transfer actually finishes, especially for multi-GB videos.
	if err := waitSettled(ctx, destPath, f.Size); err != nil {
		return 0, false, err
	}
	info, err := os.Stat(destPath)
	if err != nil {
		return 0, false, fmt.Errorf("stat after copy %s: %w", destPath, err)
	}
	if f.Size > 0 && info.Size() != f.Size {
		return 0, false, fmt.Errorf("size mismatch for %s: %d != %d", f.Name, info.Size(), f.Size)
	}
	return info.Size(), false, nil
}

// waitSettled polls until file size is stable for settleChecks consecutive
// intervals, or ctx/timeout expires.
func waitSettled(ctx context.Context, path string, expected int64) error {
	deadline := time.Now().Add(settleTimeout)
	var lastSize int64 = -1
	stable := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("settle timeout (%s) for %s", settleTimeout, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(settleInterval):
				continue
			}
		}
		size := info.Size()
		if size == lastSize && (expected <= 0 || size == expected) {
			stable++
			if stable >= settleChecks {
				return nil
			}
		} else {
			stable = 0
		}
		lastSize = size
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(settleInterval):
		}
	}
}

// Delete is unsupported for MTP — Shell delete on WPD is unreliable and
// may pop a confirmation dialog. Engine warns and skips.
func (s *Source) Delete(_ massstorage.MediaFile) error {
	return errors.New("delete not supported over MTP")
}

// SupportsDelete reports false: MTP keeps copy-only semantics (owner decision:
// move mode stays mass-storage-only for safety).
func (s *Source) SupportsDelete() bool { return false }
