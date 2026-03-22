package session

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	codeLen = 6
	// space is 36^6 = 2,176,782,336 (fits in uint32)
	space uint64 = 2176782336
	// mult must be coprime to space. space = 2^12 * 3^12, so mult can't be divisible by 2 or 3.
	// Large mult ensures consecutive IDs jump ~75% of the space apart.
	mult    uint64 = 1640531527
	multInv uint64 = 1777800055 // modular multiplicative inverse of mult mod space: (mult * multInv) % space == 1
	offset uint64 = 1013904223
)

// EncodeSessionID converts a tmux session ID ("$N") to a 6-char base36 code.
// Uses multiplicative cipher + XOR for diffusion across the full code space.
func EncodeSessionID(tmuxID string) (string, error) {
	if !strings.HasPrefix(tmuxID, "$") || len(tmuxID) < 2 {
		return "", fmt.Errorf("invalid tmux session ID: %q", tmuxID)
	}
	n, err := strconv.Atoi(tmuxID[1:])
	if err != nil {
		return "", fmt.Errorf("invalid tmux session ID: %q: %w", tmuxID, err)
	}
	if n < 0 || uint64(n) >= space {
		return "", fmt.Errorf("tmux session ID out of range: %d", n)
	}
	// Multiplicative cipher: large mult ensures consecutive IDs jump ~75% of the space
	scrambled := (uint64(n)*mult + offset) % space
	code := strconv.FormatUint(scrambled, 36)
	for len(code) < codeLen {
		code = "0" + code
	}
	return code, nil
}

// DecodeSessionID converts a 6-char base36 code back to a tmux session ID ("$N").
func DecodeSessionID(code string) (string, error) {
	if len(code) != codeLen {
		return "", fmt.Errorf("invalid session code: %q (must be %d chars)", code, codeLen)
	}
	scrambled, err := strconv.ParseUint(code, 36, 64)
	if err != nil {
		return "", fmt.Errorf("invalid session code: %q: %w", code, err)
	}
	// Reverse multiplicative cipher: n = ((scrambled - offset) * multInv) % space
	n := (((space + scrambled - offset%space) % space) * multInv) % space
	return fmt.Sprintf("$%d", n), nil
}
