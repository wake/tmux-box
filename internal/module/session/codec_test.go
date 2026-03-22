package session

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	for _, tmuxID := range []string{"$0", "$1", "$2", "$10", "$100", "$9999"} {
		code, err := EncodeSessionID(tmuxID)
		require.NoError(t, err)
		assert.Len(t, code, 6, "code should be 6 chars for %s", tmuxID)
		decoded, err := DecodeSessionID(code)
		require.NoError(t, err)
		assert.Equal(t, tmuxID, decoded, "roundtrip failed for %s", tmuxID)
	}
}

func TestEncodeProducesUniqueNonSequentialCodes(t *testing.T) {
	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code, err := EncodeSessionID(fmt.Sprintf("$%d", i))
		require.NoError(t, err)
		assert.False(t, codes[code], "duplicate code for $%d", i)
		codes[code] = true
	}
	// Verify non-sequential: $0 and $1 should produce very different codes
	code0, _ := EncodeSessionID("$0")
	code1, _ := EncodeSessionID("$1")
	shared := 0
	for i := range code0 {
		if code0[i] == code1[i] {
			shared++
		}
	}
	assert.Less(t, shared, 4, "consecutive IDs should produce different codes")
}

func TestEncodeInvalidInput(t *testing.T) {
	_, err := EncodeSessionID("invalid")
	assert.Error(t, err)
	_, err = EncodeSessionID("$")
	assert.Error(t, err)
	_, err = EncodeSessionID("")
	assert.Error(t, err)
}

func TestDecodeInvalidInput(t *testing.T) {
	_, err := DecodeSessionID("")
	assert.Error(t, err)
	_, err = DecodeSessionID("short")
	assert.Error(t, err)
	_, err = DecodeSessionID("toolong!")
	assert.Error(t, err)
}
