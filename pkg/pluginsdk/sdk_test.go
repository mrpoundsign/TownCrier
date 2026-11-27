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

type stubPlainPlugin struct {
	BasePlugin
}

func (stubPlainPlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{Uuid: "com.example.plain", Name: "Plain"}
}

type stubOverlayStylePlugin struct {
	BasePlugin
	got *pluginv1.OnOverlayStyleChangedRequest
	err error
}

func (s *stubOverlayStylePlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{Uuid: "com.example.styled", Name: "Styled"}
}

func (s *stubOverlayStylePlugin) OnOverlayStyleChanged(req *pluginv1.OnOverlayStyleChangedRequest) error {
	s.got = req
	return s.err
}

func TestDispatchOnOverlayStyleChanged(t *testing.T) {
	// Plugin without OverlayStylePlugin returns ackOK so client-side JS widgets don't fail.
	respPlain := dispatchEnvelope(stubPlainPlugin{}, nil, &pluginv1.HostEnvelope{
		RequestId: 7,
		Payload: &pluginv1.HostEnvelope_OnOverlayStyleChanged{
			OnOverlayStyleChanged: &pluginv1.OnOverlayStyleChangedRequest{WidgetId: "now_playing"},
		},
	})
	require.True(t, respPlain.GetAck().GetOk())

	// Plugin implementing OverlayStylePlugin receives the request and propagates errors cleanly.
	styled := &stubOverlayStylePlugin{}
	req := &pluginv1.OnOverlayStyleChangedRequest{
		WidgetId:     "now_playing",
		Values:       SetSettingString(nil, "accent_color", "#38BDF8"),
		CustomCss:    ".card { opacity: 0.9; }",
		UseHostTheme: true,
	}
	respStyled := dispatchEnvelope(styled, nil, &pluginv1.HostEnvelope{
		RequestId: 8,
		Payload:   &pluginv1.HostEnvelope_OnOverlayStyleChanged{OnOverlayStyleChanged: req},
	})
	require.True(t, respStyled.GetAck().GetOk())
	require.NotNil(t, styled.got)
	require.Equal(t, "now_playing", styled.got.GetWidgetId())
	require.Equal(t, "#38BDF8", GetSettingString(styled.got.GetValues(), "accent_color", ""))
	require.True(t, styled.got.GetUseHostTheme())

	styled.err = &APIError{Code: pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, Message: "bad css"}
	respErr := dispatchEnvelope(styled, nil, &pluginv1.HostEnvelope{
		RequestId: 9,
		Payload:   &pluginv1.HostEnvelope_OnOverlayStyleChanged{OnOverlayStyleChanged: req},
	})
	require.False(t, respErr.GetAck().GetOk())
	require.Equal(t, "bad css", respErr.GetAck().GetError().GetMessage())
}

