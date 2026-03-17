// internal/store/store_test.go
package store_test

import (
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/store"
)

func openTestDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	openTestDB(t)
}

func TestSessionCRUD(t *testing.T) {
	db := openTestDB(t)

	// Create
	s := store.Session{Name: "myapp", TmuxTarget: "myapp:0", Cwd: "/home/user/myapp", Mode: "term"}
	id, err := db.CreateSession(s)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("want non-zero id")
	}

	// Verify UID is generated
	got, err := db.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UID) != 8 {
		t.Errorf("want 8-char UID, got %q (len %d)", got.UID, len(got.UID))
	}

	// List
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "myapp" {
		t.Errorf("list: want [myapp], got %v", sessions)
	}

	// Update
	err = db.UpdateSession(id, store.SessionUpdate{Name: ptr("renamed")})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ = db.ListSessions()
	if sessions[0].Name != "renamed" {
		t.Errorf("update: want renamed, got %s", sessions[0].Name)
	}

	// Delete
	err = db.DeleteSession(id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ = db.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("delete: want 0 sessions, got %d", len(sessions))
	}
}

func TestDeleteNonexistent(t *testing.T) {
	db := openTestDB(t)
	err := db.DeleteSession(999)
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGroupCRUD(t *testing.T) {
	db := openTestDB(t)

	id, err := db.CreateGroup("AI Agents")
	if err != nil {
		t.Fatal(err)
	}

	groups, _ := db.ListGroups()
	if len(groups) != 1 || groups[0].Name != "AI Agents" {
		t.Errorf("list: want [AI Agents], got %v", groups)
	}

	db.UpdateGroup(id, "Renamed")
	groups, _ = db.ListGroups()
	if groups[0].Name != "Renamed" {
		t.Errorf("update: want Renamed, got %s", groups[0].Name)
	}
}

func TestGetSession(t *testing.T) {
	db := openTestDB(t)

	s := store.Session{Name: "myapp", TmuxTarget: "myapp:0", Cwd: "/tmp", Mode: "term"}
	id, _ := db.CreateSession(s)

	got, err := db.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "myapp" {
		t.Errorf("want myapp, got %s", got.Name)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := db.GetSession(999)
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestCCSessionID(t *testing.T) {
	db := openTestDB(t)

	// Create session — cc_session_id defaults to empty
	id, err := db.CreateSession(store.Session{
		Name: "test", TmuxTarget: "test:0", Cwd: "/tmp", Mode: "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get — verify default empty
	sess, err := db.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.CCSessionID != "" {
		t.Errorf("want empty cc_session_id, got %q", sess.CCSessionID)
	}

	// Update — write session ID
	ccID := "01abc234-5678-9def-0123-456789abcdef"
	err = db.UpdateSession(id, store.SessionUpdate{CCSessionID: ptr(ccID)})
	if err != nil {
		t.Fatal(err)
	}

	sess, _ = db.GetSession(id)
	if sess.CCSessionID != ccID {
		t.Errorf("want %q, got %q", ccID, sess.CCSessionID)
	}

	// List — also includes cc_session_id
	sessions, _ := db.ListSessions()
	if sessions[0].CCSessionID != ccID {
		t.Errorf("list: want %q, got %q", ccID, sessions[0].CCSessionID)
	}

	// Clear
	err = db.UpdateSession(id, store.SessionUpdate{CCSessionID: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ = db.GetSession(id)
	if sess.CCSessionID != "" {
		t.Errorf("want empty after clear, got %q", sess.CCSessionID)
	}
}

func ptr(s string) *string { return &s }
