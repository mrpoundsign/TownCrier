package audio

import (
	"encoding/hex"
	"fmt"
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

// FormatDeviceID returns the canonical lowercase hexadecimal string for a malgo.DeviceID.
// Because malgo.DeviceID implements fmt.Stringer (returning a hex string with trailing zero bytes trimmed),
// using fmt.Sprintf("%x", dev.ID) would double-hex-encode the ASCII characters of dev.ID.String().
func FormatDeviceID(id malgo.DeviceID) string {
	return strings.ToLower(id.String())
}

// MatchDeviceID reports whether a malgo.DeviceID matches targetIDHex.
// It supports canonical trimmed single-hex (id.String()), full 256-byte single-hex (hex.EncodeToString(id[:])),
// and legacy double-hex strings produced by fmt.Sprintf("%x", id) in older config files.
func MatchDeviceID(id malgo.DeviceID, targetIDHex string) bool {
	target := strings.ToLower(strings.TrimSpace(targetIDHex))
	if target == "" {
		return false
	}
	canonical := strings.ToLower(id.String())
	fullHex := strings.ToLower(hex.EncodeToString(id[:]))

	if matchSingleHexDeviceID(canonical, fullHex, target) {
		return true
	}

	// Legacy compatibility: configs saved before the fmt.Sprintf("%x", dev.ID) fix stored
	// the hex-encoded ASCII bytes of id.String() (double-hex).
	if decoded, err := hex.DecodeString(target); err == nil && len(decoded) > 0 {
		decodedStr := strings.ToLower(string(decoded))
		if matchSingleHexDeviceID(canonical, fullHex, decodedStr) {
			return true
		}
	}
	return false
}

// MatchHexDeviceIDStrings reports whether an enumerated device ID string (canonicalHex)
// matches targetIDHex, supporting both canonical single-hex and legacy double-hex IDs.
func MatchHexDeviceIDStrings(canonicalHex, targetIDHex string) bool {
	c := strings.ToLower(strings.TrimSpace(canonicalHex))
	t := strings.ToLower(strings.TrimSpace(targetIDHex))
	if c == "" || t == "" {
		return false
	}
	if c == t {
		return true
	}
	if decoded, err := hex.DecodeString(t); err == nil && len(decoded) > 0 {
		if strings.ToLower(string(decoded)) == c {
			return true
		}
	}
	return false
}

func matchSingleHexDeviceID(canonical, fullHex, candidate string) bool {
	if candidate == "" {
		return false
	}
	if candidate == canonical || candidate == fullHex {
		return true
	}
	if strings.HasPrefix(fullHex, candidate) && strings.TrimLeft(fullHex[len(candidate):], "0") == "" {
		return true
	}
	return false
}

// EnumeratePlaybackDevices returns all available audio playback devices on the host system.
func EnumeratePlaybackDevices() ([]DeviceInfo, error) {
	if testing.Testing() {
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
			ID:        FormatDeviceID(dev.ID),
			Name:      cleanName,
			IsDefault: dev.IsDefault != 0,
		}
	}
	return result, nil
}

// EnumerateCaptureDevices returns all available audio capture (microphone/input) devices on the host system.
func EnumerateCaptureDevices() ([]DeviceInfo, error) {
	if testing.Testing() {
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

	devices, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("get capture devices: %w", err)
	}

	result := make([]DeviceInfo, len(devices))
	for i, dev := range devices {
		cleanName := strings.TrimRight(dev.Name(), "\x00\r\n ")
		result[i] = DeviceInfo{
			ID:        FormatDeviceID(dev.ID),
			Name:      cleanName,
			IsDefault: dev.IsDefault != 0,
		}
	}
	return result, nil
}
