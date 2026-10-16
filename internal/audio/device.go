package audio

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/gen2brain/malgo"
)

// DeviceInfo represents an enumerated audio playback device.
type DeviceInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

// EnumeratePlaybackDevices returns all available audio playback devices on the host system.
func EnumeratePlaybackDevices() ([]DeviceInfo, error) {
	if runtime.GOOS != "windows" || testing.Testing() {
		return nil, nil
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	devices, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("get playback devices: %w", err)
	}

	result := make([]DeviceInfo, len(devices))
	for i, dev := range devices {
		cleanName := strings.TrimRight(dev.Name(), "\x00\r\n ")
		result[i] = DeviceInfo{
			ID:        fmt.Sprintf("%x", dev.ID),
			Name:      cleanName,
			IsDefault: dev.IsDefault != 0,
		}
	}
	return result, nil
}
