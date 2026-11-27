package pluginsdk

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

func TestFrameAndPreambleRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WritePreamble(&buf, 1, 0, "secret-token-123"))
	maj, minMinor, tok, err := ReadPreamble(&buf)
	require.NoError(t, err)
	require.Equal(t, uint16(1), maj)
	require.Equal(t, uint16(0), minMinor)
	require.Equal(t, "secret-token-123", tok)

	in := &pluginv1.HostEnvelope{
		RequestId: 42,
		Payload:   &pluginv1.HostEnvelope_GetManifest{GetManifest: &pluginv1.GetManifestRequest{}},
	}
	require.NoError(t, WriteFrame(&buf, in))
	var out pluginv1.HostEnvelope
	require.NoError(t, ReadFrame(&buf, &out))
	require.Equal(t, uint64(42), out.GetRequestId())
	require.NotNil(t, out.GetGetManifest())
}

func TestAPIErrorRoundTrip(t *testing.T) {
	orig := &APIError{
		Code:              pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY,
		Message:           "missing ui.v1",
		Detail:            "update host",
		RequiredMajor:     1,
		RequiredMinor:     1,
		MissingCapability: "ui.v1",
	}
	pb := ToProto(orig)
	got := FromProto(pb)
	var apiErr *APIError
	require.ErrorAs(t, got, &apiErr)
	require.Equal(t, orig.Code, apiErr.Code)
	require.Equal(t, orig.MissingCapability, apiErr.MissingCapability)
	require.Contains(t, apiErr.Error(), "missing ui.v1")
	require.Nil(t, FromProto(nil))
	require.NotNil(t, ToProto(errors.New("plain error")))
}

func TestSettingsMapHelpers(t *testing.T) {
	var m *pluginv1.SettingsMap
	m = SetSettingString(m, "name", "TownCrier")
	m = SetSettingNumber(m, "limit", 42)
	m = SetSettingBool(m, "enabled", true)
	m = SetSettingInfo(m, "status", "Connected", true)
	m = SetSettingLink(m, "auth", "https://example.com", "Authorize")
	m = SetSettingButton(m, "btn", "Click", false)

	require.Equal(t, "TownCrier", GetSettingString(m, "name", ""))
	require.Equal(t, float64(42), GetSettingNumber(m, "limit", 0))
	require.True(t, GetSettingBool(m, "enabled", false))
	require.Equal(t, "Connected", GetSettingString(m, "status", ""))
	require.Equal(t, "https://example.com", GetSettingString(m, "auth", ""))
}

