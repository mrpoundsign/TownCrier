package ui

import (
	_ "embed"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
)

//go:embed notices.txt
var thirdPartyNotices string

var (
	AppVersion = "v0.0.7"
)

const (
	AppRepository = "https://github.com/mrpoundsign/TownCrier"
	AppLicense    = "GNU Affero General Public License v3.0 (AGPL-3.0)"
)

// ShowAboutDialog displays the About TownCrier dialog with licensing, update check, and third-party notices.
func ShowAboutDialog(parent fyne.Window, sessions ...*app.Session) {
	var session *app.Session
	if len(sessions) > 0 {
		session = sessions[0]
	}

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
		widget.NewLabelWithStyle("Third-Party Notices & Open Source Attributions:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)

	noticesRich := widget.NewRichTextFromMarkdown(thirdPartyNotices)
	scrollNotices := container.NewScroll(noticesRich)
	scrollNotices.SetMinSize(fyne.NewSize(580, 260))

	content := container.NewBorder(
		header,
		nil,
		nil,
		nil,
		scrollNotices,
	)

	d := dialog.NewCustom("About TownCrier", "Close", content, parent)
	d.Resize(fyne.NewSize(620, 480))
	d.Show()
}
