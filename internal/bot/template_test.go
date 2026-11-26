package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterpolateSinglePassAndLookups(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	require.NoError(t, st.SetGlobalVar("counter", "int", "42", false))
	require.NoError(t, st.SetGlobalVar("phase", "string", "boss", true))
	require.NoError(t, st.SetUserVar("twitch", "u1", "lurkCount", "int", "7"))

	tc := TemplateContext{
		Args: map[string]string{
			"user":     "alice",
			"rawInput": "%global.counter% shouldn't re-expand",
			"arg0":     "hello",
		},
		Platform: "twitch",
		UserID:   "u1",
		Store:    st,
	}

	out := Interpolate("Hi %user% (%arg0%), count=%global.counter%, phase=%global.phase%, lurk=%user.lurkCount%, roll=%random(5,5)%, pick=%pick(only)% | %rawInput%", tc)
	require.Equal(t, "Hi alice (hello), count=42, phase=boss, lurk=7, roll=5, pick=only | %global.counter% shouldn't re-expand", out)

	// Unclosed or unknown tokens stay literal
	require.Equal(t, "100% legit %nope", Interpolate("100% legit %nope", tc))
}

func TestInterpolateOBSTokensSinglePass(t *testing.T) {
	obsCtx := TemplateContext{
		Args: map[string]string{"user": "alice"},
		LookupOBS: func(key string) (string, bool) {
			switch key {
			case "scene":
				return "Enhance %user%", true
			case "previous_scene":
				return "Main", true
			case "preview_scene":
				return "BRB", true
			case "connected":
				return "true", true
			default:
				return "", false
			}
		},
	}

	got := Interpolate("cur=%obs.scene%|alt=%obsScene%|prev=%obs.previousScene%|pre=%obs.previewScene%|conn=%obs.connected%", obsCtx)
	require.Equal(t, "cur=Enhance %user%|alt=Enhance %user%|prev=Main|pre=BRB|conn=true", got)
}

func TestInterpolateSessionAndActionTokens(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	require.NoError(t, st.SetGlobalVar("total", "int", "99", false))
	require.NoError(t, st.SetGlobalVar("deaths", "int", "3", true))

	tc := TemplateContext{
		Store: st,
		ActionVars: map[string]string{
			"localMsg": "hi %global.total%",
			"step":     "2",
		},
	}

	out := Interpolate("g=%global.total% s=%session.deaths% a=%action.step% l=%local.localMsg% miss=%action.missing%", tc)
	require.Equal(t, "g=99 s=3 a=2 l=hi %global.total% miss=", out)
}
