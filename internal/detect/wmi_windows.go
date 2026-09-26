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
	Service     string // e.g. "WUDFWpdMtp" for MTP, "WUDFWpdFs" for disk facades
	Status      string // WMI Status (OK / Error / Degraded...)
	ErrorCode   uint32 // ConfigManagerErrorCode (0 = fine; e.g. 19 = registry corrupt)
}

// cameraVIDs maps USB vendor IDs to brand names (multi-brand detection).
// cameraVIDs maps USB vendor IDs to brand names (multi-brand detection).
// Detection itself does NOT depend on this table — any WPD/PortableDevice
// is accepted; VIDs only improve the display name. Sources: usb.ids,
// libmtp device database.
var cameraVIDs = map[string]string{
	"054C": "Sony",
	"04A9": "Canon",
	"04B0": "Nikon",
	"04CB": "Fujifilm",
	"04DA": "Panasonic",
	"07B4": "OM System", // Olympus rebrand — same VID
	"2672": "GoPro",
	"2CA3": "DJI",
	"1A98": "Leica",
	"25FB": "Ricoh", // covers Pentax
	"1003": "Sigma",
	"040A": "Kodak",
	"041E": "Creative",
	"0FAD": "Blackmagic",
}

// pnpEntity is the subset of Win32_PnPEntity we need.
type pnpEntity struct {
	DeviceID               string
	Name                   string
	PNPClass               string
	Service                string
	Manufacturer           string
	Status                 string
	ConfigManagerErrorCode uint32
}

// ScanPortableDevices returns MTP/WPD portable devices (cameras in MTP mode).
// Mass-storage cameras appear as drives instead — those come via ScanDrives.
func ScanPortableDevices(ctx context.Context) ([]PortableDevice, error) {
	var devices []pnpEntity
	// Modern Windows reports MTP/WPD devices with PNPClass='WPD' and the
	// WUDFWpdMtp service. WUDFWpdFs entries are filesystem-volume facades for
	// mass-storage disks (filtered out below via USBSTOR device paths).
	// Note: WQL has no IN operator — must use explicit ORs.
	query := `SELECT DeviceID, Name, PNPClass, Service, Manufacturer, Status, ConfigManagerErrorCode FROM Win32_PnPEntity ` +
		`WHERE PNPClass = 'WPD' OR PNPClass = 'PortableDevice' OR Service = 'WUDFWpdMtp'`
	if err := wmi.Query(query, &devices); err != nil {
		return nil, err
	}

	out := make([]PortableDevice, 0, len(devices))
	for _, d := range devices {
		if strings.TrimSpace(d.DeviceID) == "" {
			continue
		}
		// Skip mass-storage disks exposed through the WPD filesystem driver —
		// they carry USBSTOR in the device path and already show up as drives.
		// Real MTP cameras enumerate under USB\VID_* with WUDFWpdMtp service.
		upper := strings.ToUpper(d.DeviceID)
		isMtpService := strings.EqualFold(d.Service, "WUDFWpdMtp")
		if strings.Contains(upper, "USBSTOR") || strings.Contains(upper, "#DISK&") {
			continue
		}
		// WUDFWpdFs entries without a real MTP service are disk facades too.
		if strings.EqualFold(d.Service, "WUDFWpdFs") && !isMtpService {
			continue
		}
		model := strings.TrimSpace(d.Name)
		// Generic WPD names ("MTP USB Device", "WPD FileSystem Volume Driver")
		// hide the real brand — prefer the VID-derived name when known.
		if brand := brandFromDeviceID(d.DeviceID); brand != "" &&
			(model == "" || strings.Contains(strings.ToUpper(model), "MTP") ||
				strings.Contains(strings.ToUpper(model), "WPD")) {
			model = brand
		}
		if model == "" {
			model = "Portable Device"
		}
		out = append(out, PortableDevice{
			PNPDeviceID: d.DeviceID,
			Model:       model,
			Service:     d.Service,
			Status:      d.Status,
			ErrorCode:   d.ConfigManagerErrorCode,
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
