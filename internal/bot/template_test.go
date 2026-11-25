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
