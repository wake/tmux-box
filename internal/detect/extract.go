package detect

import (
	"errors"
	"regexp"
)

var errNoSessionID = errors.New("session ID not found in pane content")

// sessionIDRegex matches "Session ID: <uuid>" in CC /status output.
var sessionIDRegex = regexp.MustCompile(
	`Session ID:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`,
)

// cwdRegex matches "cwd: <path>" in CC /status output.
// \S.+? requires at least one non-whitespace char to avoid matching bare "cwd: " lines.
var cwdRegex = regexp.MustCompile(`(?m)^\s*cwd:\s*(\S.+?)\s*$`)

// StatusInfo holds fields extracted from CC /status output.
type StatusInfo struct {
	SessionID string
	Cwd       string
}

// ExtractSessionID parses CC /status output to find the session UUID.
func ExtractSessionID(paneContent string) (string, error) {
	m := sessionIDRegex.FindStringSubmatch(paneContent)
	if len(m) < 2 {
		return "", errNoSessionID
	}
	return m[1], nil
}

// ExtractStatusInfo parses CC /status output for session ID and cwd.
// Session ID is required; cwd is optional (returned empty if not found).
func ExtractStatusInfo(paneContent string) (StatusInfo, error) {
	id, err := ExtractSessionID(paneContent)
	if err != nil {
		return StatusInfo{}, err
	}
	info := StatusInfo{SessionID: id}
	if m := cwdRegex.FindStringSubmatch(paneContent); len(m) >= 2 {
		info.Cwd = m[1]
	}
	return info, nil
}
