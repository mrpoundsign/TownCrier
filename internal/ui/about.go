package ui

import (
	_ "embed"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

//go:embed notices.txt
var thirdPartyNotices string

var (
	AppVersion = "v0.0.5"
)

const (
	AppRepository = "https://github.com/mrpoundsign/TownCrier"
	AppLicense    = "GNU Affero General Public License v3.0 (AGPL-3.0)"
)

// ShowAboutDialog displays the About TownCrier dialog with licensing and third-party notices.
func ShowAboutDialog(parent fyne.Window) {
	titleLabel := widget.NewLabelWithStyle("TownCrier "+AppVersion, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	descLabel := widget.NewLabelWithStyle("Open-source neural TTS for livestreamers", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	licenseLabel := widget.NewLabelWithStyle("License: "+AppLicense, fyne.TextAlignCenter, fyne.TextStyle{})

	repoURL, _ := url.Parse(AppRepository)
	repoLink := widget.NewHyperlink("GitHub: mrpoundsign/TownCrier", repoURL)
	repoLink.Alignment = fyne.TextAlignCenter

	header := container.NewVBox(
		titleLabel,
		descLabel,
		licenseLabel,
		repoLink,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Third-Party Notices & Open Source Attributions:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)

	noticesRich := widget.NewRichTextFromMarkdown(thirdPartyNotices)
	scrollNotices := container.NewScroll(noticesRich)
	scrollNotices.SetMinSize(fyne.NewSize(580, 280))

	content := container.NewBorder(
		header,
		nil,
		nil,
		nil,
		scrollNotices,
	)

	d := dialog.NewCustom("About TownCrier", "Close", content, parent)
	d.Resize(fyne.NewSize(620, 440))
	d.Show()
}
