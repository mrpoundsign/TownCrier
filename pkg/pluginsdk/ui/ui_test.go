package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

func TestBuildersAndPatchRoundTrip(t *testing.T) {
	root := Border(
		"root",
		HBox("header",
			Badge("live-badge", "LIVE", pluginv1.SemanticTone_TONE_SUCCESS),
			Spacer("hdr-sp"),
			Button("play-btn", "Play", WithIcon("play"), WithImportance(pluginv1.ButtonImportance_BUTTON_IMPORTANCE_HIGH), WithTooltip("Play track")),
		),
		Padded("footer",
			VBox("transport",
				Progress("prog", 0.42),
				Slider("seek", 0, 100, 1, 42),
			),
		),
		nil,
		nil,
		Tabs("main-tabs", "queue",
			Tab("queue", "Queue", "list",
				VBox("queue-box",
					TextInput("search", WithPlaceholder("Filter..."), WithDebounce(150)),
					Select("mode", []string{"Free", "Points"}, "Free"),
					Checkbox("approve", "Approve first", true),
					VirtualList("rows", []*pluginv1.VirtualListRow{
						ListRow("r1", HBox("row-1",
							ImageURL("thumb-1", "https://example.com/a.png", 48, 36),
							Text("title-1", "Track 1", WithBold(), WithTone(pluginv1.SemanticTone_TONE_ACCENT)),
						)),
					}, WithListMinHeight(240)),
				),
			),
			Tab("empty", "About", "info",
				Card("card-1", "CrowdDJ", "Plugin",
					EmptyState("empty-1", "Nothing queued", "Use !sr", "music", "add-btn", "Add"),
				),
			),
		),
	)

	snap := Snapshot("player", 1, root)
	raw, err := proto.Marshal(snap)
	require.NoError(t, err)

	var decoded pluginv1.UISnapshot
	require.NoError(t, proto.Unmarshal(raw, &decoded))
	require.Equal(t, "player", decoded.GetViewId())
	require.Equal(t, uint64(1), decoded.GetRevision())
	require.NotNil(t, decoded.GetRoot().GetBorder().GetCenter().GetTabs())

	batch := PatchBatch("player", 1, 2,
		SetText("title-1", "Track 1 (Playing)"),
		SetSlider("seek", 55.5),
		SetProgress("prog", 0.555),
		SetChecked("approve", false),
		SetDisabled("play-btn", true),
		Toast("Skipped!", pluginv1.SemanticTone_TONE_ACCENT),
	)
	_ = batch
}
