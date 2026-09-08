package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

func openTestFramesStore(t *testing.T) *FramesStore {
	t.Helper()
	events := openTestAgentEventStore(t)
	frames, err := events.Frames()
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	return frames
}

func TestFramesStore_UpsertAndRead(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "Sun Apr 20 01:30:00 2026")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.FrameID != frame.FrameID {
		t.Fatalf("frame_id = %q, want %q", got.FrameID, frame.FrameID)
	}
	if got.Status != agentpkg.StatusIdle {
		t.Fatalf("status = %q, want idle", got.Status)
	}
}

func TestFramesStore_NestedFrames_ParentFrameLink(t *testing.T) {
	s := openTestFramesStore(t)

	parent, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert parent: %v", err)
	}
	child, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              300,
		PPID:             200,
		ProcessStartTime: "B",
		ParentFrameID:    parent.FrameID,
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert child: %v", err)
	}

	got, err := s.GetByIdentity("%5", 300, "B")
	if err != nil {
		t.Fatalf("GetByIdentity child: %v", err)
	}
	if got.ParentFrameID != child.ParentFrameID {
		t.Fatalf("parent_frame_id = %q, want %q", got.ParentFrameID, child.ParentFrameID)
	}
}

func TestFramesStore_OrphanPolicy(t *testing.T) {
	s := openTestFramesStore(t)

	parent, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert parent: %v", err)
	}
	_, err = s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              300,
		PPID:             200,
		ProcessStartTime: "B",
		ParentFrameID:    parent.FrameID,
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert child: %v", err)
	}

	if err := s.Delete(parent.FrameID); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	child, err := s.GetByIdentity("%5", 300, "B")
	if err != nil {
		t.Fatalf("GetByIdentity child: %v", err)
	}
	if child == nil {
		t.Fatal("child frame should remain after parent delete")
	}
	if child.ParentFrameID != "" {
		t.Fatalf("parent_frame_id = %q, want empty", child.ParentFrameID)
	}
}

func TestFramesStore_UniqueOnPidAndStartTime(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame A: %v", err)
	}
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "B",
		Status:           agentpkg.StatusIdle,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame B: %v", err)
	}

	frames, err := s.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2", len(frames))
	}
}

func TestFramesStore_UpsertSameIdentityKeepsStoredFrameID(t *testing.T) {
	s := openTestFramesStore(t)

	first, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	second, err := s.Upsert(Frame{
		FrameID:          "other-id",
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             101,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if second.FrameID != first.FrameID {
		t.Fatalf("frame_id = %q, want %q", second.FrameID, first.FrameID)
	}
	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.FrameID != first.FrameID {
		t.Fatalf("stored frame_id = %q, want %q", got.FrameID, first.FrameID)
	}
	if got.PPID != 101 || got.Status != agentpkg.StatusRunning {
		t.Fatalf("stored frame = %+v, want updated fields", *got)
	}
}

func TestFrames_UpsertAndReadSubagentRefs(t *testing.T) {
	s := openTestFramesStore(t)

	want := []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}}
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        want,
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if len(got.Subagents) != 1 {
		t.Fatalf("subagents len = %d, want 1", len(got.Subagents))
	}
	// SubagentRef contains slice fields (DelegatingToolUseIDs) so == is not
	// usable; compare via reflect.DeepEqual.
	if !reflect.DeepEqual(got.Subagents[0], want[0]) {
		t.Fatalf("subagents[0] = %+v, want %+v", got.Subagents[0], want[0])
	}
}

func TestFrames_EmptySubagentsPreserved(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        nil,
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.Subagents == nil {
		t.Fatal("Subagents should be non-nil empty slice, got nil")
	}
	if len(got.Subagents) != 0 {
		t.Fatalf("Subagents len = %d, want 0", len(got.Subagents))
	}
}

func TestFrames_SubagentsJSONShapeSmoke(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "s1",
			Type:      "cc",
			StartedAt: 10,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var raw string
	err := s.db.QueryRow(`SELECT subagents_json FROM agent_frames WHERE pane_id = ? AND pid = ? AND process_start_time = ?`, "%5", 200, "A").Scan(&raw)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	for _, key := range []string{`"id"`, `"type"`, `"started_at"`, `"source_pid"`, `"source_start_time"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("subagents_json missing key %s: %s", key, raw)
		}
	}
}

// Seeds a legacy `["id"]` shape row directly via SQL (bypassing Upsert) to
// simulate an agent.sqlite that predates the Phase 2 SubagentRef schema.
func seedLegacyFrameRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"legacy-frame", "%5", "cc", 200, 100, "A",
		`["legacy-sub-1","legacy-sub-2"]`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
}

func TestMigrateFramesDB_ClearsLegacySubagentsJSON(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatalf("initial Frames: %v", err)
	}
	seedLegacyFrameRow(t, events.db)

	// Re-run migration (simulates daemon restart).
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := events.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("agent_frames count = %d after migrate, want 0 (table should be truncated)", count)
	}
}

// Seeds a row with a malformed `subagents_json` that is neither []SubagentRef
// nor []string — simulates disk corruption or an unrecognized future shape.
func seedMalformedFrameRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"malformed-frame", "%5", "cc", 300, 100, "A",
		`not-a-json`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}
}

func TestMigrateFramesDB_ClearsMixedLegacyAndNewRows(t *testing.T) {
	s := openTestFramesStore(t)

	// Insert one new-format row via Upsert.
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert new: %v", err)
	}
	// Seed one legacy row via direct SQL with distinct identity so it does
	// not collide with the new row above.
	if _, err := s.db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"legacy-frame-mixed", "%5", "cc", 201, 100, "B",
		`["legacy-id"]`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed legacy row (mixed): %v", err)
	}

	if err := migrateFramesDB(s.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("agent_frames count = %d after mixed migrate, want 0 (entire table truncated when any legacy row present)", count)
	}
}

func TestMigrateFramesDB_FailsOnMalformedSubagentsJSON(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatalf("initial Frames: %v", err)
	}
	seedMalformedFrameRow(t, events.db)

	err := migrateFramesDB(events.db)
	if err == nil {
		t.Fatal("migrateFramesDB: want error for malformed subagents_json, got nil (daemon would silently wipe unrecognized state)")
	}
	if !strings.Contains(err.Error(), "malformed subagents_json") {
		t.Fatalf("error = %q, want to mention 'malformed subagents_json'", err.Error())
	}

	// Malformed row must still be on disk — daemon refused to truncate.
	var count int
	if err := events.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent_frames count = %d after failed migrate, want 1 (malformed row preserved for manual inspection)", count)
	}
}

func TestFrames_DeleteIfUnchanged_DeletesWhenMatching(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	deleted, err := s.DeleteIfUnchanged(frame.FrameID, 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteIfUnchanged returned (false, nil); want (true, nil) for matching last_seen_at")
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got != nil {
		t.Fatalf("frame still present after DeleteIfUnchanged: %+v", *got)
	}
}

func TestFrames_DeleteIfUnchanged_SkipsWhenStale(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Simulate concurrent refresh: LastSeenAt bumped from 10 to 20 before our DELETE runs.
	if _, err := s.Upsert(Frame{
		FrameID:          frame.FrameID,
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert refresh: %v", err)
	}

	deleted, err := s.DeleteIfUnchanged(frame.FrameID, 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged returned true for stale last_seen_at; want false (concurrent refresh)")
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame missing after skip; want preserved")
	}
	if got.LastSeenAt != 20 {
		t.Fatalf("LastSeenAt = %d, want 20", got.LastSeenAt)
	}
}

func TestFrames_DeleteIfUnchanged_NotFound(t *testing.T) {
	s := openTestFramesStore(t)

	deleted, err := s.DeleteIfUnchanged("no-such-frame", 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged returned true for missing frame; want false")
	}
}

// F7 — UpsertIfUnchanged updates atomically when last_seen_at matches.
func TestFrames_UpsertIfUnchanged_UpdatesWhenMatching(t *testing.T) {
	s := openTestFramesStore(t)

	stored, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	stored.Subagents = append(stored.Subagents, agentpkg.SubagentRef{ID: "s2", Type: "codex", StartedAt: 20})
	stored.LastSeenAt = 20
	ok, got, err := s.UpsertIfUnchanged(stored, 10)
	if err != nil {
		t.Fatalf("UpsertIfUnchanged: %v", err)
	}
	if !ok {
		t.Fatal("UpsertIfUnchanged returned false with matching baseline; want true")
	}
	if len(got.Subagents) != 2 {
		t.Fatalf("Subagents len = %d, want 2 (s1 + s2 merged atomically)", len(got.Subagents))
	}
	if got.LastSeenAt != 20 {
		t.Fatalf("LastSeenAt = %d, want 20 (refreshed)", got.LastSeenAt)
	}
}

// F8 — UpsertIfUnchanged returns (false, zero, nil) when a concurrent writer
// has moved last_seen_at past the provided baseline.
func TestFrames_UpsertIfUnchanged_SkipsWhenStale(t *testing.T) {
	s := openTestFramesStore(t)

	stored, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Simulate a concurrent writer bumping last_seen_at to 30.
	racer := stored
	racer.LastSeenAt = 30
	racer.Subagents = append(racer.Subagents, agentpkg.SubagentRef{ID: "racer", Type: "codex", StartedAt: 25})
	if _, err := s.Upsert(racer); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Our baseline is still 10; UpsertIfUnchanged should refuse.
	stored.Subagents = append(stored.Subagents, agentpkg.SubagentRef{ID: "stale", Type: "opencode", StartedAt: 15})
	stored.LastSeenAt = 20
	ok, got, err := s.UpsertIfUnchanged(stored, 10)
	if err != nil {
		t.Fatalf("UpsertIfUnchanged: %v", err)
	}
	if ok {
		t.Fatal("UpsertIfUnchanged returned true with stale baseline; want false")
	}
	if got.FrameID != "" {
		t.Fatalf("stored.FrameID = %q, want zero Frame (stale)", got.FrameID)
	}

	// Verify the racer's write is still in the DB, not clobbered.
	row, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if row == nil {
		t.Fatal("frame missing")
	}
	if row.LastSeenAt != 30 {
		t.Fatalf("LastSeenAt = %d, want 30 (racer preserved)", row.LastSeenAt)
	}
	// Racer's subagent list had 2 entries (s1 + racer). Our stale write
	// should not have clobbered them with our (s1 + stale) version.
	if len(row.Subagents) != 2 {
		t.Fatalf("Subagents len = %d, want 2 (racer version preserved)", len(row.Subagents))
	}
	foundRacer := false
	for _, ref := range row.Subagents {
		if ref.ID == "racer" {
			foundRacer = true
		}
		if ref.ID == "stale" {
			t.Fatal("stale write clobbered racer (UpsertIfUnchanged invariant violated)")
		}
	}
	if !foundRacer {
		t.Fatal("racer ref missing from DB after UpsertIfUnchanged conflict")
	}
}

// F9 — UpsertIfUnchanged requires frame.FrameID; rejects zero-ID insert path.
func TestFrames_UpsertIfUnchanged_RejectsEmptyFrameID(t *testing.T) {
	s := openTestFramesStore(t)
	_, _, err := s.UpsertIfUnchanged(Frame{PaneID: "%5", PID: 100, ProcessStartTime: "A"}, 0)
	if err == nil {
		t.Fatal("UpsertIfUnchanged with empty FrameID should error; got nil")
	}
}

func TestMigrateFramesDB_PreservesNewSubagentsJSON(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Re-run migration: new-format row must survive.
	if err := migrateFramesDB(s.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent_frames count = %d after migrate, want 1 (new-format row should survive)", count)
	}
}

// --- Task 1: the frame's own session identity -----------------------------

func seedIdentityFrame(t *testing.T, s *FramesStore, sessionID, cwd string) Frame {
	t.Helper()
	frame, err := s.Upsert(Frame{
		PaneID:           "%9",
		AgentType:        "cc",
		PID:              900,
		PPID:             800,
		ProcessStartTime: "S",
		SessionID:        sessionID,
		Cwd:              cwd,
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	return frame
}

func assertStoredIdentity(t *testing.T, s *FramesStore, frameID, wantSession, wantCwd string) {
	t.Helper()
	var gotSession, gotCwd string
	if err := s.db.QueryRow(`SELECT session_id, cwd FROM agent_frames WHERE frame_id = ?`, frameID).
		Scan(&gotSession, &gotCwd); err != nil {
		t.Fatalf("read identity columns: %v", err)
	}
	if gotSession != wantSession || gotCwd != wantCwd {
		t.Fatalf("stored identity = (%q, %q), want (%q, %q)", gotSession, gotCwd, wantSession, wantCwd)
	}
}

func TestFrames_UpsertPersistsSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)

	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")
	if frame.SessionID != "sess-1" || frame.Cwd != "/tmp/work" {
		t.Fatalf("returned frame identity = (%q, %q), want (sess-1, /tmp/work)", frame.SessionID, frame.Cwd)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")

	got, err := s.GetByIdentity("%9", 900, "S")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.SessionID != "sess-1" || got.Cwd != "/tmp/work" {
		t.Fatalf("read identity = (%q, %q), want (sess-1, /tmp/work)", got.SessionID, got.Cwd)
	}
}

func TestFrames_UpsertWithEmptyIdentityKeepsStored(t *testing.T) {
	s := openTestFramesStore(t)
	seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	again, err := s.Upsert(Frame{
		PaneID:           "%9",
		AgentType:        "cc",
		PID:              900,
		PPID:             800,
		ProcessStartTime: "S",
		Status:           agentpkg.StatusRunning,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if again.SessionID != "sess-1" || again.Cwd != "/tmp/work" {
		t.Fatalf("returned frame identity = (%q, %q), want (sess-1, /tmp/work)", again.SessionID, again.Cwd)
	}
	assertStoredIdentity(t, s, again.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_UpsertWithNonEmptyIdentityDoesNotOverwrite(t *testing.T) {
	s := openTestFramesStore(t)
	seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	again, err := s.Upsert(Frame{
		PaneID:           "%9",
		AgentType:        "cc",
		PID:              900,
		PPID:             800,
		ProcessStartTime: "S",
		SessionID:        "sess-STALE",
		Cwd:              "/tmp/STALE",
		Status:           agentpkg.StatusRunning,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	// The DO UPDATE SET list omits both columns, and the method returns the
	// re-selected row — so the caller sees the *stored* values, not its own.
	if again.SessionID != "sess-1" || again.Cwd != "/tmp/work" {
		t.Fatalf("returned frame identity = (%q, %q), want stored (sess-1, /tmp/work)", again.SessionID, again.Cwd)
	}
	assertStoredIdentity(t, s, again.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_UpdateSessionIdentity_SetsBoth(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "", "")

	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-2", "/srv/app", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-2", "/srv/app")
}

func TestFrames_UpdateSessionIdentity_WritesOnlyNonEmpty(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-2", "", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity session-only: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-2", "/tmp/work")

	if err := s.UpdateSessionIdentity(frame.FrameID, "", "/srv/app", 200); err != nil {
		t.Fatalf("UpdateSessionIdentity cwd-only: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-2", "/srv/app")
}

func TestFrames_UpdateSessionIdentity_NoOpForTwoEmptyArgs(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	if err := s.UpdateSessionIdentity(frame.FrameID, "", "", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity empty: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")

	// No SQL runs at all, so even an unknown frame id is not an error here —
	// the call is a pure no-op.
	if err := s.UpdateSessionIdentity("no-such-frame", "", "", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity empty on unknown frame = %v, want nil (no SQL runs)", err)
	}
}

func TestFrames_UpdateSessionIdentity_UnknownFrame(t *testing.T) {
	s := openTestFramesStore(t)
	seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	err := s.UpdateSessionIdentity("no-such-frame", "sess-2", "/srv/app", 100)
	if err != sql.ErrNoRows {
		t.Fatalf("UpdateSessionIdentity unknown frame = %v, want sql.ErrNoRows", err)
	}
}

// The event version is what orders identity writes, so a write carrying an
// older one is refused outright rather than applied last-writer-wins.
func TestFrames_UpdateSessionIdentity_OlderVersionIsRefused(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "", "")

	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-new", "/srv/new", 300); err != nil {
		t.Fatalf("UpdateSessionIdentity newer: %v", err)
	}
	err := s.UpdateSessionIdentity(frame.FrameID, "sess-old", "/srv/old", 200)
	if !errors.Is(err, ErrIdentityOutOfOrder) {
		t.Fatalf("UpdateSessionIdentity older = %v, want ErrIdentityOutOfOrder", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-new", "/srv/new")

	// A refusal is NOT the frame going missing: those two are answered
	// differently because callers log them differently.
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a stale write reported the frame as gone")
	}
}

// Equal versions still apply, so a retry of the same event is idempotent
// rather than a refusal.
func TestFrames_UpdateSessionIdentity_SameVersionStillApplies(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "", "")

	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-1", "/srv/one", 300); err != nil {
		t.Fatalf("UpdateSessionIdentity first: %v", err)
	}
	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-1", "/srv/two", 300); err != nil {
		t.Fatalf("UpdateSessionIdentity retry: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/srv/two")
}

// The version is carried by a column of its own. Ordering identity writes by
// last_seen_at would let an unrelated writer — a proxy attach, a probe status
// transition — reorder them.
func TestFrames_UpdateSessionIdentity_VersionIsIndependentOfLastSeenAt(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "", "")

	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-new", "/srv/new", 300); err != nil {
		t.Fatalf("UpdateSessionIdentity newer: %v", err)
	}
	if err := s.UpdateStatusAndLastSeen(frame.FrameID, agentpkg.StatusRunning, 9999); err != nil {
		t.Fatalf("UpdateStatusAndLastSeen: %v", err)
	}
	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-old", "/srv/old", 200); !errors.Is(err, ErrIdentityOutOfOrder) {
		t.Fatalf("UpdateSessionIdentity older = %v, want ErrIdentityOutOfOrder", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-new", "/srv/new")
}

func TestFrames_UpsertIfUnchanged_LeavesSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	frame.SessionID = "sess-STALE"
	frame.Cwd = "/tmp/STALE"
	frame.Status = agentpkg.StatusRunning
	frame.LastSeenAt = 20
	ok, stored, err := s.UpsertIfUnchanged(frame, 10)
	if err != nil {
		t.Fatalf("UpsertIfUnchanged: %v", err)
	}
	if !ok {
		t.Fatal("UpsertIfUnchanged reported a stale baseline, want applied")
	}
	if stored.SessionID != "sess-1" || stored.Cwd != "/tmp/work" {
		t.Fatalf("returned identity = (%q, %q), want stored (sess-1, /tmp/work)", stored.SessionID, stored.Cwd)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_UpdateHookPath_LeavesSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	frame.SessionID = "sess-STALE"
	frame.Cwd = "/tmp/STALE"
	frame.Status = agentpkg.StatusRunning
	frame.LastSeenAt = 20
	if err := s.UpdateHookPath(frame); err != nil {
		t.Fatalf("UpdateHookPath: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_UpdateHookPathAndResetSubagents_LeavesSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	frame.SessionID = "sess-STALE"
	frame.Cwd = "/tmp/STALE"
	frame.Status = agentpkg.StatusRunning
	frame.LastSeenAt = 20
	if err := s.UpdateHookPathAndResetSubagents(frame); err != nil {
		t.Fatalf("UpdateHookPathAndResetSubagents: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_UpdateStatusAndLastSeen_LeavesSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	if err := s.UpdateStatusAndLastSeen(frame.FrameID, agentpkg.StatusRunning, 20); err != nil {
		t.Fatalf("UpdateStatusAndLastSeen: %v", err)
	}
	assertStoredIdentity(t, s, frame.FrameID, "sess-1", "/tmp/work")
}

func TestFrames_AllReadersReturnSessionIdentity(t *testing.T) {
	s := openTestFramesStore(t)
	frame := seedIdentityFrame(t, s, "sess-1", "/tmp/work")

	byIdentity, err := s.GetByIdentity("%9", 900, "S")
	if err != nil || byIdentity == nil {
		t.Fatalf("GetByIdentity: %v (frame=%v)", err, byIdentity)
	}
	if byIdentity.SessionID != "sess-1" || byIdentity.Cwd != "/tmp/work" {
		t.Fatalf("GetByIdentity identity = (%q, %q), want (sess-1, /tmp/work)", byIdentity.SessionID, byIdentity.Cwd)
	}

	byPanePID, err := s.FindByPanePID("%9", 900)
	if err != nil || byPanePID == nil {
		t.Fatalf("FindByPanePID: %v (frame=%v)", err, byPanePID)
	}
	if byPanePID.SessionID != "sess-1" || byPanePID.Cwd != "/tmp/work" {
		t.Fatalf("FindByPanePID identity = (%q, %q), want (sess-1, /tmp/work)", byPanePID.SessionID, byPanePID.Cwd)
	}

	byPane, err := s.ListByPane("%9")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(byPane) != 1 || byPane[0].SessionID != "sess-1" || byPane[0].Cwd != "/tmp/work" {
		t.Fatalf("ListByPane = %+v, want one frame with (sess-1, /tmp/work)", byPane)
	}

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 || all[0].SessionID != "sess-1" || all[0].Cwd != "/tmp/work" {
		t.Fatalf("ListAll = %+v, want one frame with (sess-1, /tmp/work)", all)
	}
	if all[0].FrameID != frame.FrameID {
		t.Fatalf("ListAll frame_id = %q, want %q", all[0].FrameID, frame.FrameID)
	}
}

// seedPreIdentitySchema creates agent_frames exactly as it looked before the
// session_id / cwd columns existed, plus one row, so migrateFramesDB has to
// take the additive ALTER path.
func seedPreIdentitySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE agent_frames (
			frame_id            TEXT PRIMARY KEY,
			pane_id             TEXT NOT NULL,
			agent_type          TEXT NOT NULL,
			pid                 INTEGER NOT NULL,
			ppid                INTEGER NOT NULL,
			process_start_time  TEXT NOT NULL,
			parent_frame_id     TEXT,
			subagents_json      TEXT NOT NULL DEFAULT '[]',
			status              TEXT NOT NULL,
			started_at          INTEGER NOT NULL,
			last_seen_at        INTEGER NOT NULL,
			verified            INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (parent_frame_id) REFERENCES agent_frames(frame_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create pre-identity schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"old-frame", "%9", "cc", 900, 800, "S", `[]`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed pre-identity row: %v", err)
	}
}

func TestMigrateFramesDB_AddsIdentityColumnsToOldSchema(t *testing.T) {
	events := openTestAgentEventStore(t)
	seedPreIdentitySchema(t, events.db)

	frames, err := events.Frames()
	if err != nil {
		t.Fatalf("Frames (migrate): %v", err)
	}
	// Idempotent: a second migration must not fail on already-added columns.
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatalf("migrateFramesDB second run: %v", err)
	}

	all, err := frames.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll returned %d frames, want the pre-existing row preserved", len(all))
	}
	if all[0].FrameID != "old-frame" {
		t.Fatalf("frame_id = %q, want old-frame", all[0].FrameID)
	}
	if all[0].SessionID != "" || all[0].Cwd != "" {
		t.Fatalf("migrated identity = (%q, %q), want two empty strings", all[0].SessionID, all[0].Cwd)
	}
	// identity_seq arrives on the same ALTER path and defaults to 0, which is
	// older than any event — so the first identity write on a migrated row
	// lands rather than being refused as stale.
	if err := frames.UpdateSessionIdentity("old-frame", "sess-1", "/w/old", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity on a migrated row: %v", err)
	}
	assertStoredIdentity(t, frames, "old-frame", "sess-1", "/w/old")
}

// The real upgrade path for an install that already has the identity columns:
// only identity_seq is missing, and the per-column guard has to add that one
// without tripping over the two that are already there.
func TestMigrateFramesDB_AddsOnlyTheMissingIdentityColumn(t *testing.T) {
	events := openTestAgentEventStore(t)
	seedPreIdentitySchema(t, events.db)
	for _, column := range []string{"session_id", "cwd"} {
		if _, err := events.db.Exec(`ALTER TABLE agent_frames ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatalf("pre-add %s: %v", column, err)
		}
	}

	frames, err := events.Frames()
	if err != nil {
		t.Fatalf("Frames (migrate): %v", err)
	}
	if err := frames.UpdateSessionIdentity("old-frame", "sess-1", "/w/old", 100); err != nil {
		t.Fatalf("UpdateSessionIdentity after partial migration: %v", err)
	}
	assertStoredIdentity(t, frames, "old-frame", "sess-1", "/w/old")
}

// --- where the version comes from -------------------------------------------

// storedIdentitySeq reads the version column directly, which nothing else in
// these tests does: every other assertion is about the identity the version
// let through, and this one is about the version itself.
func storedIdentitySeq(t *testing.T, s *FramesStore, frameID string) int64 {
	t.Helper()
	var seq int64
	if err := s.db.QueryRow(`SELECT identity_seq FROM agent_frames WHERE frame_id = ?`, frameID).Scan(&seq); err != nil {
		t.Fatalf("read identity_seq: %v", err)
	}
	return seq
}

// TestFrames_NextIdentitySeq_ResumesFromThePersistedMaximum — the counter has
// to survive a restart, and the reason is not tidiness.
//
// A counter that starts from zero on every boot hands the first event after a
// restart a version BELOW every version already stored, and
// UpdateSessionIdentity refuses those: the daemon would come back up unable to
// record an identity for any frame that outlived it, silently, until the
// counter climbed back past the old high-water mark. That is the objection
// that made an earlier round reach for the wall clock instead — and it is an
// objection to a counter that forgets, not to counters.
func TestFrames_NextIdentitySeq_ResumesFromThePersistedMaximum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.db")

	events, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := events.Frames()
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	frame := seedIdentityFrame(t, s, "", "")
	if err := s.UpdateSessionIdentity(frame.FrameID, "sess-before", "/before", 5000); err != nil {
		t.Fatalf("UpdateSessionIdentity: %v", err)
	}
	events.Close()

	reopened, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	s2, err := reopened.Frames()
	if err != nil {
		t.Fatalf("frames after restart: %v", err)
	}

	next := s2.NextIdentitySeq()
	if next <= 5000 {
		t.Fatalf("NextIdentitySeq() = %d after a restart, want > 5000 — the daemon came back unable to write an identity", next)
	}
	if err := s2.UpdateSessionIdentity(frame.FrameID, "sess-after", "/after", next); err != nil {
		t.Fatalf("the first write after a restart was refused: %v", err)
	}
	assertStoredIdentity(t, s2, frame.FrameID, "sess-after", "/after")
}

// TestFrames_NextIdentitySeq_IsMonotonicAndNotTheClock — the version is a
// counter, and that is the whole point: a wall clock can go backwards, and one
// that does hands a NEWER event a SMALLER version, so the newer event's write
// is refused (or, worse, an older event holding a version allocated before the
// step still overwrites it afterwards). Nothing about the ordering guard in
// UpdateSessionIdentity can detect that; it can only compare the numbers it is
// given.
//
// Two properties, and both are needed: consecutive allocations differ by
// exactly one — so the value cannot be a timestamp, whatever the clock does —
// and concurrent allocations are all distinct, since the events being ordered
// are by definition in flight at the same time.
func TestFrames_NextIdentitySeq_IsMonotonicAndNotTheClock(t *testing.T) {
	s := openTestFramesStore(t)

	first, second := s.NextIdentitySeq(), s.NextIdentitySeq()
	if second != first+1 {
		t.Fatalf("consecutive allocations = %d, %d: want a counter stepping by one, not a clock reading", first, second)
	}

	const goroutines = 64
	seen := make(chan int64, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen <- s.NextIdentitySeq()
		}()
	}
	wg.Wait()
	close(seen)

	unique := make(map[int64]bool, goroutines)
	for v := range seen {
		if unique[v] {
			t.Fatalf("NextIdentitySeq handed %d out twice: two in-flight events cannot be ordered against each other", v)
		}
		unique[v] = true
		if v <= second {
			t.Fatalf("NextIdentitySeq went backwards: %d after %d", v, second)
		}
	}
}
