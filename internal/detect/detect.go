// Package detect watches for connected cameras.
// Two source kinds:
//   - mass_storage: removable drive letters containing DCIM/PRIVATE folders
//   - mtp:          portable devices (WPD) visible via Win32_PnPEntity
//
// The Watcher polls on an interval and emits connect/disconnect callbacks.
package detect

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Mode identifies how the camera exposes its media.
type Mode string

const (
	ModeMassStorage Mode = "mass_storage"
	ModeMTP         Mode = "mtp"
)

// Device is a detected camera source.
type Device struct {
	ID          string // stable id: "ms:E:" or "mtp:<pnp device id>"
	Model       string // friendly model name
	Mode        Mode
	DriveLetter string // mass storage only, e.g. "E:\\"
	PNPDeviceID string // raw PNP id (mtp) or correlated disk id
	Label       string // volume label (mass storage)
	// MTP only: driver health — "Error" with ErrorCode means the OS can't
	// expose the device to Shell (e.g. Code 19 registry corruption).
	DriverStatus string
	DriverError  uint32
}

// Hooks receives device events.
type Hooks struct {
	OnConnect    func(dev Device)
	OnDisconnect func(dev Device)
	OnError      func(err error)
}

// Watcher polls for devices and diffs state.
type Watcher struct {
	mu       sync.Mutex
	devices  map[string]Device
	hooks    Hooks
	interval time.Duration
	scan     func(ctx context.Context) ([]Device, error)
}

// NewWatcher creates a watcher using the platform scan implementation.
func NewWatcher(interval time.Duration, hooks Hooks) *Watcher {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Watcher{
		devices:  map[string]Device{},
		hooks:    hooks,
		interval: interval,
		scan:     ScanDevices,
	}
}

// Devices returns a copy of currently connected devices.
func (w *Watcher) Devices() []Device {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Device, 0, len(w.devices))
	for _, d := range w.devices {
		out = append(out, d)
	}
	return out
}

// Run polls until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	w.scanOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanOnce(ctx)
		}
	}
}

func (w *Watcher) scanOnce(ctx context.Context) {
	found, err := w.scan(ctx)
	if err != nil {
		if w.hooks.OnError != nil {
			w.hooks.OnError(err)
		}
		return
	}
	current := make(map[string]Device, len(found))
	for _, d := range found {
		current[d.ID] = d
	}

	w.mu.Lock()
	for id, dev := range current {
		if _, ok := w.devices[id]; !ok {
			w.devices[id] = dev
			if w.hooks.OnConnect != nil {
				w.hooks.OnConnect(dev)
			}
		}
	}
	for id, dev := range w.devices {
		if _, ok := current[id]; !ok {
			delete(w.devices, id)
			if w.hooks.OnDisconnect != nil {
				w.hooks.OnDisconnect(dev)
			}
		}
	}
	w.mu.Unlock()
}

// ScanDevices is set per-platform; Windows impl in wmi_windows.go + drives_windows.go.
var ScanDevices = func(ctx context.Context) ([]Device, error) {
	drives, err := ScanDrives(ctx)
	if err != nil {
		return nil, err
	}
	portables, _ := ScanPortableDevices(ctx) // WMI errors are non-fatal; drives still work
	CorrelateDriveModels(drives, portables)

	out := make([]Device, 0, len(drives)+len(portables))
	for _, d := range drives {
		model := d.Model
		if strings.TrimSpace(model) == "" {
			model = strings.TrimSpace(d.Label)
		}
		if model == "" {
			model = "Camera Card"
		}
		out = append(out, Device{
			ID:          "ms:" + d.Letter,
			Model:       model,
			Mode:        ModeMassStorage,
			DriveLetter: d.Letter,
			Label:       d.Label,
			PNPDeviceID: d.PNPDeviceID,
		})
	}
	for _, p := range portables {
		out = append(out, Device{
			ID:           "mtp:" + p.PNPDeviceID,
			Model:        p.Model,
			Mode:         ModeMTP,
			PNPDeviceID:  p.PNPDeviceID,
			DriverStatus: p.Status,
			DriverError:  p.ErrorCode,
		})
	}
	return out, nil
}
