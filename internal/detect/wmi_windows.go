//go:build windows

package detect

import (
	"context"
	"strings"

	"github.com/StackExchange/wmi"
)

// PortableDevice is a WPD/MTP device visible in Win32_PnPEntity.
type PortableDevice struct {
	PNPDeviceID string
	Model       string
	Service     string // e.g. "WUDFWpdFs" for MTP, "USBSTOR" for mass storage
}

// cameraVIDs maps USB vendor IDs to brand names (multi-brand detection).
var cameraVIDs = map[string]string{
	"054C": "Sony",
	"04A9": "Canon",
	"04B0": "Nikon",
	"04CB": "Fujifilm",
	"04DA": "Panasonic",
	"07B4": "Olympus",
	"2672": "GoPro",
	"2CA3": "DJI",
	"041E": "Creative",
	"0FAD": "Blackmagic",
}

// pnpEntity is the subset of Win32_PnPEntity we need.
type pnpEntity struct {
	DeviceID     string
	Name         string
	PNPClass     string
	Service      string
	Manufacturer string
}

// ScanPortableDevices returns MTP/WPD portable devices (cameras in MTP mode).
// Mass-storage cameras appear as drives instead — those come via ScanDrives.
func ScanPortableDevices(ctx context.Context) ([]PortableDevice, error) {
	var devices []pnpEntity
	// PNPClass=PortableDevice is the canonical WPD class; also catch devices
	// served by the WPD filesystem driver.
	query := `SELECT DeviceID, Name, PNPClass, Service, Manufacturer FROM Win32_PnPEntity ` +
		`WHERE PNPClass = 'PortableDevice' OR Service = 'WUDFWpdFs'`
	if err := wmi.Query(query, &devices); err != nil {
		return nil, err
	}

	out := make([]PortableDevice, 0, len(devices))
	for _, d := range devices {
		if strings.TrimSpace(d.DeviceID) == "" {
			continue
		}
		model := strings.TrimSpace(d.Name)
		if model == "" {
			model = brandFromDeviceID(d.DeviceID)
		}
		if model == "" {
			model = "Portable Device"
		}
		out = append(out, PortableDevice{
			PNPDeviceID: d.DeviceID,
			Model:       model,
			Service:     d.Service,
		})
	}
	return out, nil
}

// diskModel is the subset of Win32_DiskDrive used for drive correlation.
type diskModel struct {
	DeviceID string
	Model    string
}

// CorrelateDriveModels fills Drive.Model/PNPDeviceID from Win32_DiskDrive +
// Win32_LogicalDiskToPartition mapping. Best-effort: failures leave Model empty.
func CorrelateDriveModels(drives []Drive, _ []PortableDevice) {
	if len(drives) == 0 {
		return
	}
	models := map[string]string{} // drive letter -> disk model
	pnpIDs := map[string]string{}

	type diskPartition struct {
		Antecedent string // Win32_DiskDrive path
		Dependent  string // Win32_DiskPartition path
	}
	type logicalToPartition struct {
		Antecedent string // Win32_DiskPartition path
		Dependent  string // Win32_LogicalDisk path (DeviceID=E:)
	}
	type logicalDisk struct {
		DeviceID string
	}
	type partition struct {
		DeviceID string
	}
	type diskDrive struct {
		DeviceID string
		Model    string
	}

	var disks []diskDrive
	if err := wmi.Query(`SELECT DeviceID, Model FROM Win32_DiskDrive WHERE MediaType LIKE '%Removable%' OR InterfaceType='USB'`, &disks); err != nil {
		return
	}
	var d2p []diskPartition
	if err := wmi.Query(`SELECT Antecedent, Dependent FROM Win32_DiskDriveToDiskPartition`, &d2p); err != nil {
		return
	}
	var p2l []logicalToPartition
	if err := wmi.Query(`SELECT Antecedent, Dependent FROM Win32_LogicalDiskToPartition`, &p2l); err != nil {
		return
	}

	// partition -> logical letter
	partToLetter := map[string]string{}
	for _, l := range p2l {
		letter := wmiRefValue(l.Dependent)
		part := wmiRefValue(l.Antecedent)
		partToLetter[part] = letter
	}
	// disk -> partition -> letter
	for _, dp := range d2p {
		disk := wmiRefValue(dp.Antecedent)
		part := wmiRefValue(dp.Dependent)
		letter := partToLetter[part]
		if letter == "" {
			continue
		}
		for _, dk := range disks {
			if strings.EqualFold(wmiRefValue(`"`+dk.DeviceID+`"`), disk) || strings.EqualFold(dk.DeviceID, disk) {
				models[letter+`\`] = strings.TrimSpace(dk.Model)
				pnpIDs[letter+`\`] = dk.DeviceID
			}
		}
	}

	for i := range drives {
		if m, ok := models[strings.ToUpper(drives[i].Letter)]; ok {
			drives[i].Model = m
			drives[i].PNPDeviceID = pnpIDs[drives[i].Letter]
		}
	}
}

// wmiRefValue extracts the quoted device path from a WMI association reference,
// e.g. `\\PC\root\cimv2:Win32_LogicalDisk.DeviceID="E:"` -> `E:`.
func wmiRefValue(ref string) string {
	if i := strings.LastIndexByte(ref, '"'); i > 0 {
		inner := ref[:i]
		if j := strings.LastIndexByte(inner, '"'); j >= 0 {
			return inner[j+1:]
		}
	}
	if i := strings.LastIndexByte(ref, '='); i >= 0 {
		return strings.Trim(ref[i+1:], `"`)
	}
	return ref
}

// brandFromDeviceID returns the camera brand for a known VID, "" otherwise.
func brandFromDeviceID(deviceID string) string {
	upper := strings.ToUpper(deviceID)
	for vid, brand := range cameraVIDs {
		if strings.Contains(upper, "VID_"+vid) {
			return brand + " Camera"
		}
	}
	return ""
}

// IsKnownCameraVID reports whether the device id contains a camera brand VID.
func IsKnownCameraVID(deviceID string) bool {
	return brandFromDeviceID(deviceID) != ""
}
