package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func main() {
	myApp := app.NewWithID("com.github.mrpoundsign.octochorpebot")
	myWindow := myApp.NewWindow("OctochorpeBot")

	title := widget.NewLabelWithStyle("OctochorpeBot", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	status := widget.NewLabel("Multi-Platform Chat Voice Bot")

	myWindow.SetContent(container.NewVBox(
		title,
		status,
	))
	myWindow.Resize(fyne.NewSize(600, 400))
	myWindow.ShowAndRun()
}
