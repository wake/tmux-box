package session

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	xorKey  = 0x5A3C7E1D
	codeLen = 6
	maxBits = 31
)

// Compile-time assertion: xorKey must fit in maxBits
var _ = uint(xorKey >> maxBits) // panics at compile time if xorKey has bit 31+ set

// bitPerm is a fixed bit permutation table for obfuscation.
var bitPerm = [31]int{
	17, 5, 23, 11, 29, 2, 19, 8, 26, 14, 1,
	20, 7, 25, 13, 30, 3, 21, 9, 27, 15, 0,
	18, 6, 24, 12, 28, 4, 22, 10, 16,
}

var bitPermInv [31]int

func init() {
	for i, p := range bitPerm {
		bitPermInv[p] = i
	}
}

func shuffleBits(n int, perm [31]int) int {
	var result int
	for i := 0; i < maxBits; i++ {
		if n&(1<<i) != 0 {
			result |= 1 << perm[i]
		}
	}
	return result
}

// EncodeSessionID converts a tmux session ID ("$N") to a 6-char base36 code.
func EncodeSessionID(tmuxID string) (string, error) {
	if !strings.HasPrefix(tmuxID, "$") || len(tmuxID) < 2 {
		return "", fmt.Errorf("invalid tmux session ID: %q", tmuxID)
	}
	n, err := strconv.Atoi(tmuxID[1:])
	if err != nil {
		return "", fmt.Errorf("invalid tmux session ID: %q: %w", tmuxID, err)
	}
	if n < 0 || n >= (1<<maxBits) {
		return "", fmt.Errorf("tmux session ID out of range: %d", n)
	}
	scrambled := shuffleBits(n^xorKey, bitPerm)
	code := strconv.FormatInt(int64(scrambled), 36)
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
	scrambled, err := strconv.ParseInt(code, 36, 64)
	if err != nil {
		return "", fmt.Errorf("invalid session code: %q: %w", code, err)
	}
	n := shuffleBits(int(scrambled), bitPermInv) ^ xorKey
	if n < 0 {
		return "", fmt.Errorf("decoded negative value from code: %q", code)
	}
	return fmt.Sprintf("$%d", n), nil
}
