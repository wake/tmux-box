// internal/store/meta_test.go
package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/store"
)

func TestMetaStoreGetSetDelete(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	// Initially empty
	metas, err := ms.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)

	// Set meta
	err = ms.SetMeta("$0", store.SessionMeta{Mode: "term"})
	require.NoError(t, err)

	// Get meta
	meta, err := ms.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "term", meta.Mode)

	// Update meta
	mode := "stream"
	ccID := "session-123"
	err = ms.UpdateMeta("$0", store.MetaUpdate{Mode: &mode, CCSessionID: &ccID})
	require.NoError(t, err)
	meta, _ = ms.GetMeta("$0")
	assert.Equal(t, "stream", meta.Mode)
	assert.Equal(t, "session-123", meta.CCSessionID)

	// Delete meta
	err = ms.DeleteMeta("$0")
	require.NoError(t, err)
	meta, _ = ms.GetMeta("$0")
	assert.Nil(t, meta)
}

func TestMetaStoreGetMissing(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	meta, err := ms.GetMeta("$999")
	require.NoError(t, err)
	assert.Nil(t, meta)
}

func TestMetaStoreCleanOrphans(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ms.SetMeta("$0", store.SessionMeta{Mode: "term"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "stream"})
	ms.SetMeta("$2", store.SessionMeta{Mode: "term"})
	removed, err := ms.CleanOrphans([]string{"$0", "$2"})
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	meta, _ := ms.GetMeta("$1")
	assert.Nil(t, meta, "$1 should be cleaned")
}

func TestMetaStoreCleanOrphansEmptySlice(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	// Populate some meta
	ms.SetMeta("$0", store.SessionMeta{Mode: "term", CCSessionID: "abc"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "stream", CCSessionID: "def"})

	// Empty liveTmuxIDs means tmux unavailable — should NOT delete anything
	removed, err := ms.CleanOrphans([]string{})
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	// Both records must survive
	m0, _ := ms.GetMeta("$0")
	m1, _ := ms.GetMeta("$1")
	assert.NotNil(t, m0, "$0 should survive empty-slice cleanup")
	assert.NotNil(t, m1, "$1 should survive empty-slice cleanup")
}

func TestMetaStoreCleanOrphansNilSlice(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()

	ms.SetMeta("$0", store.SessionMeta{Mode: "term"})

	// nil also means tmux unavailable
	removed, err := ms.CleanOrphans(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	m0, _ := ms.GetMeta("$0")
	assert.NotNil(t, m0, "$0 should survive nil-slice cleanup")
}

func TestMetaStoreResetStaleModes(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ms.SetMeta("$0", store.SessionMeta{Mode: "stream"})
	ms.SetMeta("$1", store.SessionMeta{Mode: "jsonl"})
	ms.SetMeta("$2", store.SessionMeta{Mode: "term"})
	err = ms.ResetStaleModes()
	require.NoError(t, err)
	m0, _ := ms.GetMeta("$0")
	m1, _ := ms.GetMeta("$1")
	m2, _ := ms.GetMeta("$2")
	assert.Equal(t, "term", m0.Mode)
	assert.Equal(t, "term", m1.Mode)
	assert.Equal(t, "term", m2.Mode)
}
