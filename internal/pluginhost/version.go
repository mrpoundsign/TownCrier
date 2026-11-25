package pluginhost

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const (
	// HostAPIMajor is the plugin protocol major version supported by this TownCrier build.
	HostAPIMajor uint32 = 1
	// HostAPIMinor is the plugin protocol minor version supported by this TownCrier build.
	HostAPIMinor uint32 = 0
	// HostAPIPatch is the plugin protocol patch version supported by this TownCrier build.
	HostAPIPatch uint32 = 0
)

// SupportedCapabilities lists the capability identifiers provided by this TownCrier build (v1.0.0).
var SupportedCapabilities = []string{
	"core.v1",
	"events.v1",
	"kv.v1",
	"http.v1",
	"tts.v1",
	"overlay.v1",
}

// HostVersionString returns the formatted host plugin API version (e.g. "1.0.0").
func HostVersionString() string {
	return fmt.Sprintf("%d.%d.%d", HostAPIMajor, HostAPIMinor, HostAPIPatch)
}

// FormatVersion formats a ProtocolVersion for UI display.
func FormatVersion(v *pluginv1.ProtocolVersion) string {
	if v == nil || v.GetMajor() == 0 {
		return "unspecified"
	}
	return fmt.Sprintf("v%d.%d.%d", v.GetMajor(), v.GetMinor(), v.GetPatch())
}

// CheckVersion validates a plugin's (major, minHostMinor) against the host's supported ABI.
func CheckVersion(pluginMajor, minHostMinor uint32) *pluginv1.APIError {
	if pluginMajor == 0 {
		return &pluginv1.APIError{
			Code:          pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message:       fmt.Sprintf("Plugin reported invalid API major version 0; TownCrier requires API v%d.x", HostAPIMajor),
			RequiredMajor: HostAPIMajor,
			RequiredMinor: HostAPIMinor,
		}
	}
	if pluginMajor < HostAPIMajor {
		return &pluginv1.APIError{
			Code:          pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message:       fmt.Sprintf("Plugin targets legacy API v%d (current host is v%s) — please update the plugin", pluginMajor, HostVersionString()),
			RequiredMajor: HostAPIMajor,
			RequiredMinor: HostAPIMinor,
		}
	}
	if pluginMajor > HostAPIMajor {
		return &pluginv1.APIError{
			Code:          pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message:       fmt.Sprintf("Plugin requires newer TownCrier API v%d (current host is v%s) — please update TownCrier", pluginMajor, HostVersionString()),
			RequiredMajor: pluginMajor,
			RequiredMinor: minHostMinor,
		}
	}
	if minHostMinor > HostAPIMinor {
		return &pluginv1.APIError{
			Code:          pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message:       fmt.Sprintf("Plugin requires TownCrier API v%d.%d+ (current host is v%s) — please update TownCrier", pluginMajor, minHostMinor, HostVersionString()),
			RequiredMajor: pluginMajor,
			RequiredMinor: minHostMinor,
		}
	}
	return nil
}

// CheckCapabilities verifies all capabilities requested by a plugin manifest are supported by this host.
func CheckCapabilities(required []string) *pluginv1.APIError {
	for _, rawCap := range required {
		capName := strings.TrimSpace(rawCap)
		if capName == "" {
			continue
		}
		if !slices.Contains(SupportedCapabilities, capName) {
			return &pluginv1.APIError{
				Code:              pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
				Message:           fmt.Sprintf("Plugin requires capability %q which is not supported by TownCrier API v%s", capName, HostVersionString()),
				Detail:            "Supported capabilities: " + strings.Join(SupportedCapabilities, ", "),
				RequiredMajor:     HostAPIMajor,
				RequiredMinor:     HostAPIMinor,
				MissingCapability: capName,
			}
		}
	}
	return nil
}

// NewHandshakeResponse builds a HandshakeResponse after validating the TCRP preamble.
func NewHandshakeResponse(apiErr *pluginv1.APIError) *pluginv1.HandshakeResponse {
	if apiErr != nil {
		return &pluginv1.HandshakeResponse{
			Ok:                    false,
			Error:                 apiErr,
			HostMajor:             HostAPIMajor,
			HostMinor:             HostAPIMinor,
			HostPatch:             HostAPIPatch,
			SupportedCapabilities: slices.Clone(SupportedCapabilities),
		}
	}
	return &pluginv1.HandshakeResponse{
		Ok:                    true,
		HostMajor:             HostAPIMajor,
		HostMinor:             HostAPIMinor,
		HostPatch:             HostAPIPatch,
		SupportedCapabilities: slices.Clone(SupportedCapabilities),
	}
}
