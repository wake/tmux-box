// internal/history/history.go
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

var commandNameRegex = regexp.MustCompile(`<command-name>(.+?)</command-name>`)

// CCProjectPath converts a working directory path to CC's project hash format.
// Example: "/Users/wake/Workspace" → "-Users-wake-Workspace"
func CCProjectPath(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// ParseJSONL reads CC JSONL session data and returns stream-json compatible messages.
// Only user and assistant messages are included. maxBytes limits total input read.
// When the input exceeds maxBytes, earlier messages are dropped (tail is preserved).
func ParseJSONL(r io.Reader, maxBytes int64) ([]map[string]interface{}, error) {
	if maxBytes <= 0 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	// If data exceeds maxBytes, keep the tail — skip partial first line.
	if int64(len(data)) > maxBytes {
		data = data[len(data)-int(maxBytes):]
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var messages []map[string]interface{}
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip malformed lines
		}

		typ, _ := entry["type"].(string)
		if typ != "user" && typ != "assistant" {
			continue
		}

		// Skip isMeta messages (CC system bookkeeping)
		if isMeta, ok := entry["isMeta"].(bool); ok && isMeta {
			continue
		}

		msg, ok := entry["message"].(map[string]interface{})
		if !ok {
			continue
		}

		// Skip synthetic assistant messages (e.g. "No response requested.")
		if typ == "assistant" {
			if model, _ := msg["model"].(string); model == "<synthetic>" {
				continue
			}
		}

		// Filter CC internal markup in user string content (BEFORE normalization)
		if typ == "user" {
			if contentStr, ok := msg["content"].(string); ok {
				if strings.HasPrefix(contentStr, "<local-command-stdout>") ||
					strings.HasPrefix(contentStr, "<local-command-caveat>") {
					continue
				}
				// Parse <command-name>/exit</command-name> → "/exit"
				if m := commandNameRegex.FindStringSubmatch(contentStr); len(m) >= 2 {
					contentStr = m[1]
				}
				msg["content"] = contentStr // will be normalized below
			}
		}

		// Normalize content: string → content block array
		if content, ok := msg["content"].(string); ok {
			msg["content"] = []interface{}{
				map[string]interface{}{"type": "text", "text": content},
			}
		}

		messages = append(messages, map[string]interface{}{
			"type":    typ,
			"message": msg,
		})
	}
	return messages, scanner.Err()
}
