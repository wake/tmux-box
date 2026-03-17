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

// ExtractSessionID parses CC /status output to find the session UUID.
func ExtractSessionID(paneContent string) (string, error) {
	m := sessionIDRegex.FindStringSubmatch(paneContent)
	if len(m) < 2 {
		return "", errNoSessionID
	}
	return m[1], nil
}
