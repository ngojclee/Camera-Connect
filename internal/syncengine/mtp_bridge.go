package syncengine

import (
	"fmt"

	"github.com/ngojclee/camera-connect/internal/camera/mtp"
	"github.com/ngojclee/camera-connect/internal/detect"
)

// openMTPSource creates the MediaSource for an MTP device.
func openMTPSource(dev detect.Device) (MediaSource, error) {
	src, err := mtp.Open(dev.PNPDeviceID, dev.Model)
	if err != nil {
		return nil, fmt.Errorf("open mtp source %s: %w", dev.ID, err)
	}
	return src, nil
}
