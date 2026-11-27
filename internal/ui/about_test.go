package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"
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

func TestAboutDialogApplicationLogTab(t *testing.T) {
	w := test.NewWindow(nil)
	defer w.Close()

	tmpDir := t.TempDir()
	tmpLogPath := filepath.Join(tmpDir, "towncrier.log")

	var openedDir string
	origOpenFolder := openFolderFunc
	openFolderFunc = func(dir string) error {
		openedDir = dir
		return nil
	}
	t.Cleanup(func() {
		openFolderFunc = origOpenFolder
	})

	v := newAboutDialogView(w, nil, tmpLogPath)
	require.Equal(t, v.noticesTab, v.tabs.Selected())
	require.Empty(t, v.logContentLabel.Text)

	// Selecting Application Log on a missing file shows friendly placeholder.
	v.tabs.Select(v.logTab)
	require.Equal(t, "(No application log file found yet.)", v.logContentLabel.Text)

	// Writing log lines and clicking Refresh shows log content and status.
	firstLog := "2026/11/25 23:55:00 main.go:10: TownCrier started\n2026/11/25 23:55:01 main.go:11: Ready\n"
	require.NoError(t, os.WriteFile(tmpLogPath, []byte(firstLog), 0o600))
	test.Tap(v.refreshBtn)
	require.Contains(t, v.logContentLabel.Text, "TownCrier started")
	require.Contains(t, v.logContentLabel.Text, "Ready")
	require.Equal(t, "Refreshed", v.logStatusLabel.Text)
	require.True(t, v.logStatusLabel.Visible())

	// Copy populates clipboard with raw log text.
	test.Tap(v.copyBtn)
	require.Equal(t, firstLog, fyne.CurrentApp().Clipboard().Content())
	require.Equal(t, "Copied to clipboard", v.logStatusLabel.Text)

	// Open Log Folder invokes openFolderFunc with log directory.
	test.Tap(v.openFolderBtn)
	require.Equal(t, tmpDir, openedDir)
	require.Equal(t, "Opened log folder", v.logStatusLabel.Text)

	// Large log exceeding 64 KB shows truncation banner and newest entry.
	var sb strings.Builder
	for range 2500 {
		sb.WriteString("2026/11/25 23:55:02 verbose log line filler entry for tail check\n")
	}
	sb.WriteString("2026/11/25 23:59:59 final newest tail log line\n")
	require.NoError(t, os.WriteFile(tmpLogPath, []byte(sb.String()), 0o600))
	test.Tap(v.refreshBtn)
	require.Contains(t, v.logContentLabel.Text, "showing latest 64 KB of log")
	require.Contains(t, v.logContentLabel.Text, "final newest tail log line")
}

func assertNoCodeBlockSegments(t *testing.T, segments []widget.RichTextSegment) {
	t.Helper()
	for _, s := range segments {
		_, isCodeBlock := s.(*widget.CodeBlockSegment)
		require.False(t, isCodeBlock, "expected CodeBlockSegment to be flattened into a wrapped TextSegment")
		if listSeg, ok := s.(*widget.ListSegment); ok {
			assertNoCodeBlockSegments(t, listSeg.Items)
		}
	}
}

func TestAboutDialogNoticesFlattenedAndWrapped(t *testing.T) {
	w := test.NewWindow(nil)
	defer w.Close()

	v := newAboutDialogView(w, nil, filepath.Join(t.TempDir(), "towncrier.log"))
	require.NotNil(t, v.noticesRich)
	require.Equal(t, fyne.TextWrapWord, v.noticesRich.Wrapping)
	assertNoCodeBlockSegments(t, v.noticesRich.Segments)

	rendered := v.noticesRich.String()
	require.Contains(t, rendered, "The MIT License (MIT)")
	require.Contains(t, rendered, "Apache License")
	require.Contains(t, rendered, "Redistribution and use in source")
}
