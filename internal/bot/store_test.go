package bot

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreGlobalEphemeralUserAndGroups(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	// Global persistent
	require.NoError(t, st.SetGlobalVar("death_count", "int", "3", false))
	v, found := st.GetGlobalVar("death_count", false)
	require.True(t, found)
	require.Equal(t, "3", v.Value)

	next, err := st.IncrementGlobalVar("death_count", 2, false)
	require.NoError(t, err)
	require.InDelta(t, 5.0, next, 0.001)

	require.Len(t, st.ListGlobalVars(false), 1)
	require.NoError(t, st.DeleteGlobalVar("death_count", false))
	_, found = st.GetGlobalVar("death_count", false)
	require.False(t, found)

	// Ephemeral
	require.NoError(t, st.SetGlobalVar("round", "int", "1", true))
	nextEph, err := st.IncrementGlobalVar("round", 4, true)
	require.NoError(t, err)
	require.InDelta(t, 5.0, nextEph, 0.001)
	require.Len(t, st.ListGlobalVars(true), 1)
	require.NoError(t, st.DeleteGlobalVar("round", true))
	require.Empty(t, st.ListGlobalVars(true))

	// User vars
	require.NoError(t, st.SetUserVar("Twitch", "u123", "points", "int", "100"))
	uv, found := st.GetUserVar("twitch", "u123", "points")
	require.True(t, found)
	require.Equal(t, "100", uv.Value)

	nextUser, err := st.IncrementUserVar("twitch", "u123", "points", 25)
	require.NoError(t, err)
	require.InDelta(t, 125.0, nextUser, 0.001)
	require.Len(t, st.ListUserVars("twitch", "u123"), 1)
	require.NoError(t, st.DeleteUserVar("twitch", "u123", "points"))
	_, found = st.GetUserVar("twitch", "u123", "points")
	require.False(t, found)

	// Groups
	require.NoError(t, st.AddUserToGroup("Regulars", "twitch", "u123", "Alice"))
	require.True(t, st.IsUserInGroup("regulars", "twitch", "u123"))
	require.False(t, st.IsUserInGroup("vip", "twitch", "u123"))
	require.Equal(t, []string{"Regulars"}, st.UserGroups("twitch", "u123"))
	require.Equal(t, []string{"Regulars"}, st.ListGroups())
	require.Len(t, st.ListGroupMembers("Regulars"), 1)

	require.NoError(t, st.RemoveUserFromGroup("Regulars", "twitch", "u123"))
	require.False(t, st.IsUserInGroup("Regulars", "twitch", "u123"))
}

func TestStoreConcurrentIncrement(t *testing.T) {
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = st.IncrementGlobalVar("hits", 1, true)
		})
	}
	wg.Wait()
	v, ok := st.GetGlobalVar("hits", true)
	require.True(t, ok)
	require.Equal(t, "20", v.Value)
}
