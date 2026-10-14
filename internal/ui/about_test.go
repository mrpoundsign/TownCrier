package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestAboutDialogContent(t *testing.T) {
	if len(thirdPartyNotices) == 0 {
		t.Fatal("expected thirdPartyNotices to be embedded and non-empty")
	}

	requiredStrings := []string{
		"Microsoft ONNX Runtime",
		"sherpa-onnx",
		"Kokoro-82M",
		"Fyne GUI Toolkit",
		"malgo / miniaudio",
		"go-twitch-irc",
		"go-keyring",
	}

	for _, req := range requiredStrings {
		if !strings.Contains(thirdPartyNotices, req) {
			t.Errorf("expected thirdPartyNotices to contain %q", req)
		}
	}

	w := test.NewWindow(nil)
	defer w.Close()

	ShowAboutDialog(w)
}
