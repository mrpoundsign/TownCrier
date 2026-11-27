package ui

import (
	_ "embed"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/logging"
)

//go:embed notices.txt
var thirdPartyNotices string

var (
	AppVersion = "v0.2.0"
)

const (
	AppRepository         = "https://github.com/mrpoundsign/TownCrier"
	AppLicense            = "GNU Affero General Public License v3.0 (AGPL-3.0)"
	aboutLogMaxBytes      = logging.DefaultTailMaxBytes
	aboutCompactTextScale = float32(0.80)
)

var openFolderFunc = openFolderInOS

func openFolderInOS(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

type aboutDialogView struct {
	root            fyne.CanvasObject
	tabs            *container.AppTabs
	noticesTab      *container.TabItem
	noticesRich     *widget.RichText
	logTab          *container.TabItem
	logPathLabel    *widget.Label
	logContentLabel *widget.Label
	logScroll       *container.Scroll
	logStatusLabel  *widget.Label
	refreshBtn      *widget.Button
	copyBtn         *widget.Button
	openFolderBtn   *widget.Button
	logPath         string
	lastLogText     string
	refreshLog      func(showStatus bool)
}

// flattenNoticeCodeBlocks converts markdown CodeBlockSegments (which render inside nested HScroll
// containers that look like text inputs and trap vertical mouse-wheel scrolling) into plain wrapped
// monospace TextSegments.
func flattenNoticeCodeBlocks(segments []widget.RichTextSegment) []widget.RichTextSegment {
	out := make([]widget.RichTextSegment, 0, len(segments))
	for _, s := range segments {
		switch seg := s.(type) {
		case *widget.CodeBlockSegment:
			out = append(out, &widget.TextSegment{
				Text: seg.Text,
				Style: widget.RichTextStyle{
					TextStyle: fyne.TextStyle{Monospace: true},
					ColorName: theme.ColorNameForeground,
					SizeName:  theme.SizeNameText,
				},
			})
		case *widget.ListSegment:
			seg.Items = flattenNoticeCodeBlocks(seg.Items)
			out = append(out, seg)
		default:
			out = append(out, s)
		}
	}
	return out
}

func newAboutDialogView(parent fyne.Window, session *app.Session, logPathOverride string) *aboutDialogView {
	titleLabel := widget.NewLabelWithStyle("TownCrier "+AppVersion, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	descLabel := widget.NewLabelWithStyle("Open-source neural TTS for livestreamers", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	licenseLabel := widget.NewLabelWithStyle("License: "+AppLicense, fyne.TextAlignCenter, fyne.TextStyle{})

	repoURL, _ := url.Parse(AppRepository)
	repoLink := widget.NewHyperlink("GitHub: mrpoundsign/TownCrier", repoURL)
	repoLink.Alignment = fyne.TextAlignCenter

	updateStatus := widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	updateStatus.Hide()

	checkUpdateBtn := widget.NewButtonWithIcon("Check for Updates", theme.ViewRefreshIcon(), func() {
		updateStatus.SetText("Checking GitHub Releases...")
		updateStatus.Show()
		TriggerManualUpdateCheck(parent, session, func(msg string) {
			updateStatus.SetText(msg)
			updateStatus.Show()
		})
	})

	header := container.NewVBox(
		titleLabel,
		descLabel,
		licenseLabel,
		repoLink,
		container.NewCenter(checkUpdateBtn),
		updateStatus,
		widget.NewSeparator(),
	)

	noticesRich := widget.NewRichTextFromMarkdown(thirdPartyNotices)
	noticesRich.Segments = flattenNoticeCodeBlocks(noticesRich.Segments)
	noticesRich.Wrapping = fyne.TextWrapWord
	noticesRich.Refresh()
	scrollNotices := container.NewVScroll(container.NewThemeOverride(noticesRich, NewScaledChatTheme(aboutCompactTextScale)))
	scrollNotices.SetMinSize(fyne.NewSize(640, 280))

	logPath := strings.TrimSpace(logPathOverride)
	if logPath == "" {
		logPath = logging.LogPath()
	}

	v := &aboutDialogView{
		noticesRich: noticesRich,
		logPath:     logPath,
	}

	v.logPathLabel = widget.NewLabelWithStyle("Log file: "+logPath, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	v.logPathLabel.Truncation = fyne.TextTruncateEllipsis

	v.logStatusLabel = widget.NewLabelWithStyle("", fyne.TextAlignTrailing, fyne.TextStyle{Italic: true})
	v.logStatusLabel.Hide()

	v.logContentLabel = widget.NewLabel("")
	v.logContentLabel.TextStyle = fyne.TextStyle{Monospace: true}
	v.logContentLabel.Wrapping = fyne.TextWrapWord

	v.logScroll = container.NewVScroll(container.NewThemeOverride(v.logContentLabel, NewScaledChatTheme(aboutCompactTextScale)))
	v.logScroll.SetMinSize(fyne.NewSize(640, 260))

	v.refreshLog = func(showStatus bool) {
		content, truncated, err := logging.ReadTail(v.logPath, aboutLogMaxBytes)
		switch {
		case errors.Is(err, os.ErrNotExist):
			v.lastLogText = ""
			v.logContentLabel.SetText("(No application log file found yet.)")
		case err != nil:
			v.lastLogText = ""
			v.logContentLabel.SetText("Unable to read application log: " + err.Error())
		case strings.TrimSpace(content) == "":
			v.lastLogText = ""
			v.logContentLabel.SetText("(Application log is currently empty.)")
		case truncated:
			v.lastLogText = content
			v.logContentLabel.SetText("... showing latest 64 KB of log (use Open Log Folder for full file) ...\n\n" + content)
		default:
			v.lastLogText = content
			v.logContentLabel.SetText(content)
		}
		v.logScroll.ScrollToBottom()
		if showStatus {
			v.logStatusLabel.SetText("Refreshed")
			v.logStatusLabel.Show()
		}
	}

	v.refreshBtn = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshLog(true)
	})

	v.copyBtn = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		if v.lastLogText == "" {
			v.refreshLog(false)
		}
		if v.lastLogText == "" {
			v.logStatusLabel.SetText("Nothing to copy")
			v.logStatusLabel.Show()
			return
		}
		if appInst := fyne.CurrentApp(); appInst != nil && appInst.Clipboard() != nil {
			appInst.Clipboard().SetContent(v.lastLogText)
			v.logStatusLabel.SetText("Copied to clipboard")
			v.logStatusLabel.Show()
			return
		}
		v.logStatusLabel.SetText("Clipboard unavailable")
		v.logStatusLabel.Show()
	})

	v.openFolderBtn = widget.NewButtonWithIcon("Open Log Folder", theme.FolderOpenIcon(), func() {
		dir := filepath.Dir(v.logPath)
		if err := openFolderFunc(dir); err != nil {
			v.logStatusLabel.SetText("Could not open folder: " + err.Error())
			v.logStatusLabel.Show()
			return
		}
		v.logStatusLabel.SetText("Opened log folder")
		v.logStatusLabel.Show()
	})

	logHeader := container.NewVBox(
		v.logPathLabel,
		container.NewHBox(v.refreshBtn, v.copyBtn, v.openFolderBtn, layout.NewSpacer(), v.logStatusLabel),
	)
	logTabBody := container.NewBorder(logHeader, nil, nil, nil, v.logScroll)

	v.noticesTab = container.NewTabItem("Third-Party Notices", scrollNotices)
	v.logTab = container.NewTabItem("Application Log", logTabBody)
	v.tabs = container.NewAppTabs(v.noticesTab, v.logTab)
	v.tabs.OnSelected = func(item *container.TabItem) {
		if item == v.logTab {
			v.refreshLog(false)
		}
	}

	v.root = container.NewBorder(
		header,
		nil,
		nil,
		nil,
		v.tabs,
	)
	return v
}

// ShowAboutDialog displays the About TownCrier dialog with licensing, update check, third-party notices, and application logs.
func ShowAboutDialog(parent fyne.Window, sessions ...*app.Session) {
	var session *app.Session
	if len(sessions) > 0 {
		session = sessions[0]
	}

	v := newAboutDialogView(parent, session, "")
	d := dialog.NewCustom("About TownCrier", "Close", v.root, parent)
	d.Resize(fyne.NewSize(720, 540))
	d.Show()
}
