// internal/server/history_handler_test.go
package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/store"
)

func TestHistoryEmptyCCSessionID(t *testing.T) {
	db, srv := setupServerWithDB(t)

	// Create session without cc_session_id
	id, _ := db.CreateSession(store.Session{Name: "hist-test", Cwd: "/tmp", Mode: "stream"})

	resp, err := http.Get(fmt.Sprintf("%s/api/sessions/%d/history", srv.URL, id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var messages []interface{}
	json.NewDecoder(resp.Body).Decode(&messages)
	if len(messages) != 0 {
		t.Fatalf("want empty array, got %d messages", len(messages))
	}
}

func TestHistoryReturnsMessages(t *testing.T) {
	db, srv := setupServerWithDB(t)

	// Create temp CC JSONL file
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	ccSessionID := "test-session-uuid"
	projectHash := "-tmp" // CCProjectPath("/tmp")
	jsonlDir := filepath.Join(homeDir, ".claude", "projects", projectHash)
	os.MkdirAll(jsonlDir, 0755)

	jsonlContent := `{"type":"progress","data":"ignore"}
{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn"}}
`
	os.WriteFile(filepath.Join(jsonlDir, ccSessionID+".jsonl"), []byte(jsonlContent), 0644)

	// Create session with cc_session_id and cwd=/tmp
	id, _ := db.CreateSession(store.Session{Name: "hist-msg", Cwd: "/tmp", Mode: "stream"})
	ccID := ccSessionID
	db.UpdateSession(id, store.SessionUpdate{CCSessionID: &ccID})

	resp, err := http.Get(fmt.Sprintf("%s/api/sessions/%d/history", srv.URL, id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var messages []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&messages)

	if len(messages) != 2 {
		t.Fatalf("want 2 messages (user + assistant), got %d", len(messages))
	}
	if messages[0]["type"] != "user" {
		t.Fatalf("msg 0: want user, got %v", messages[0]["type"])
	}
	if messages[1]["type"] != "assistant" {
		t.Fatalf("msg 1: want assistant, got %v", messages[1]["type"])
	}
}

func TestHistoryNotFound(t *testing.T) {
	_, srv := setupServerWithDB(t)

	resp, err := http.Get(fmt.Sprintf("%s/api/sessions/999/history", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}
