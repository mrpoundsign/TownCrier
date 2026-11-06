package ui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/mrpoundsign/appinstaller/pkg/updater"
	"github.com/mrpoundsign/appinstaller/pkg/updaterui"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
)

// NewTownCrierUpdater returns a GitHub Releases Checker configured for mrpoundsign/TownCrier.
func NewTownCrierUpdater() *updater.Checker {
	return updater.NewChecker("mrpoundsign", "TownCrier")
}

// ShowTownCrierUpdateDialog opens the Fyne Update Available dialog for a discovered release.
func ShowTownCrierUpdateDialog(parent fyne.Window, session *app.Session, rel *updater.ReleaseInfo) {
	if parent == nil || rel == nil {
		return
	}
	checker := NewTownCrierUpdater()
	updaterui.ShowUpdateDialog(updaterui.DialogConfig{
		Parent:         parent,
		AppName:        "TownCrier",
		CurrentVersion: AppVersion,
		Release:        rel,
		Checker:        checker,
		OnSkipVersion: func(ver string) {
			if session != nil {
				_ = session.UpdateConfig(func(c *config.Config) {
					c.Update.SkippedVersion = ver
				})
			}
		},
		OnQuitApp: func() {
			if session != nil {
				_ = session.Close()
			}
			if a := fyne.CurrentApp(); a != nil {
				a.Quit()
			}
		},
	})
}

// TriggerManualUpdateCheck queries GitHub Releases asynchronously and either notifies the user
// that TownCrier is up to date or opens the Update Available dialog.
func TriggerManualUpdateCheck(parent fyne.Window, session *app.Session, onStatus func(msg string)) {
	if testing.Testing() {
		return
	}
	if onStatus != nil {
		onStatus("Checking GitHub Releases for updates...")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()

		checker := NewTownCrierUpdater()
		rel, isNewer, err := checker.CheckLatest(ctx, AppVersion)
		fyne.Do(func() {
			if err != nil {
				msg := "⚠️ Update check failed: " + err.Error()
				if onStatus != nil {
					onStatus(msg)
				}
				return
			}
			if !isNewer || rel == nil {
				msg := "✓ TownCrier " + AppVersion + " is up to date."
				if onStatus != nil {
					onStatus(msg)
				} else if parent != nil {
					dialog.ShowInformation("Up to Date", msg, parent)
				}
				return
			}

			if onStatus != nil {
				onStatus("⬆ Update available: TownCrier v" + rel.Version)
			}
			ShowTownCrierUpdateDialog(parent, session, rel)
		})
	}()
}
