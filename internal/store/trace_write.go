package store

// The trace store's write path: chain upsert, step inserts, root-payload dedup
// planning and retention.
//
// SaveChain performs all of those inside one transaction, which is why pruning
// lives here rather than with the migration code.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// storedTracePayload is one step's payload as it will be written. Payload is
// already in stored form, so the caller passes it straight to SQL: when IsRoot
// is set it is the empty string, because the bytes live on the chain row
// instead. Routing it back through rawJSONText would expand that empty string
// to "null" and store four pointless bytes.
type storedTracePayload struct {
	Payload string
	IsRoot  bool
}

// tracePayloadPlan is how a chain's payloads are to be stored: the shared root
// once on the chain row, and one entry per step in the order they were given.
type tracePayloadPlan struct {
	RootPayload string
	Steps       []storedTracePayload
}

// planTraceRootDedup decides which steps share the chain's root payload, so
// that payload can be stored once on the chain row instead of once per step.
//
// steps must already be in read order — normalizeTraceRecord ends with a
// sort.SliceStable on Seq → CreatedAt → StepID, exactly matching the read
// query's ORDER BY — so the root candidate is simply steps[0]. Seq is not
// unique, which is why those tie-breaks matter.
//
// Comparison is on the *stored* form (rawJSONText), never on the raw
// json.RawMessage: only payloads whose stored bytes are equal may be merged, so
// the read path can return byte-identical values. Semantically equal but
// byte-different payloads simply keep their own copies.
//
// RootPayload is "null" when dedup is off, which is also the column default.
// Correctness never depends on that value: a chain whose steps all have a
// genuine null payload legitimately stores "null" as its root with the flags
// set, so only the per-step flag distinguishes the two cases.
func planTraceRootDedup(steps []TraceStep) tracePayloadPlan {
	plan := tracePayloadPlan{
		RootPayload: "null",
		Steps:       make([]storedTracePayload, len(steps)),
	}
	for i := range steps {
		plan.Steps[i].Payload = rawJSONText(steps[i].PayloadJSON)
	}
	if len(steps) == 0 {
		return plan
	}

	candidate := plan.Steps[0].Payload
	matches := 0
	for _, step := range plan.Steps {
		if step.Payload == candidate {
			matches++
		}
	}
	if matches < 2 {
		return plan
	}

	plan.RootPayload = candidate
	for i := range plan.Steps {
		if plan.Steps[i].Payload == candidate {
			plan.Steps[i].Payload = ""
			plan.Steps[i].IsRoot = true
		}
	}
	return plan
}

// SaveChain stores a chain and its steps atomically, replacing any existing
// record for the same chain_id.
func (s *TraceStore) SaveChain(record TraceRecord) (err error) {
	if s == nil || s.db == nil {
		return fmt.Errorf("trace store is nil")
	}
	chain, steps, err := normalizeTraceRecord(record)
	if err != nil {
		return err
	}
	plan := planTraceRootDedup(steps)

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM agent_trace_steps WHERE chain_id = ?`, chain.ChainID); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at,
			root_payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chain_id) DO UPDATE SET
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			terminal_status = excluded.terminal_status,
			terminal_reason = excluded.terminal_reason,
			tmux_session = excluded.tmux_session,
			pane_id = excluded.pane_id,
			root_agent_type = excluded.root_agent_type,
			root_event_name = excluded.root_event_name,
			root_reason = excluded.root_reason,
			latest_step_kind = excluded.latest_step_kind,
			latest_decision = excluded.latest_decision,
			latest_step_reason = excluded.latest_step_reason,
			step_count = excluded.step_count,
			updated_at = excluded.updated_at,
			root_payload_json = excluded.root_payload_json
	`, chain.ChainID, chain.StartedAt, chain.CompletedAt, chain.TerminalStatus, chain.TerminalReason,
		chain.TmuxSession, chain.PaneID, chain.RootAgentType, chain.RootEventName, chain.RootReason,
		chain.LatestStepKind, chain.LatestDecision, chain.LatestStepReason, chain.StepCount, time.Now().UnixNano(),
		plan.RootPayload); err != nil {
		return err
	}

	for i, step := range steps {
		// The plan already holds each payload in stored form, so it goes to SQL
		// as-is — see storedTracePayload for why a deduped step's empty string
		// must not be routed back through rawJSONText.
		stored := plan.Steps[i]
		isRoot := 0
		if stored.IsRoot {
			isRoot = 1
		}
		if _, err = tx.Exec(`
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
				agent_type, frame_id, parent_frame_id, event_name, decision, reason,
				payload_json, before_json, after_json, created_at, payload_is_root
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, step.StepID, step.ChainID, nullString(step.ParentStepID), step.Seq, step.Kind, step.TmuxSession, step.PaneID,
			step.AgentType, step.FrameID, step.ParentFrameID, step.EventName, step.Decision, step.Reason,
			stored.Payload, rawJSONText(step.BeforeJSON), rawJSONText(step.AfterJSON), step.CreatedAt, isRoot); err != nil {
			return err
		}
	}

	maxChains, maxSteps := s.traceLimits()
	if err = pruneTraceChains(tx, maxChains, maxSteps); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *TraceStore) traceLimits() (int, int) {
	maxChains := s.maxChains
	if maxChains <= 0 {
		maxChains = defaultTraceMaxChains
	}
	maxSteps := s.maxSteps
	if maxSteps <= 0 {
		maxSteps = defaultTraceMaxSteps
	}
	return maxChains, maxSteps
}

func normalizeTraceRecord(record TraceRecord) (TraceChain, []TraceStep, error) {
	chain := record.Chain
	if chain.ChainID == "" {
		chain.ChainID = uuid.NewString()
	}

	steps := make([]TraceStep, len(record.Steps))
	copy(steps, record.Steps)
	for i := range steps {
		if steps[i].StepID == "" {
			steps[i].StepID = uuid.NewString()
		}
		if steps[i].ChainID == "" {
			steps[i].ChainID = chain.ChainID
		}
		if steps[i].ChainID != chain.ChainID {
			return TraceChain{}, nil, fmt.Errorf("step %s belongs to chain %s, want %s", steps[i].StepID, steps[i].ChainID, chain.ChainID)
		}
		if steps[i].Seq == 0 {
			steps[i].Seq = i + 1
		}
		if steps[i].TmuxSession == "" {
			steps[i].TmuxSession = chain.TmuxSession
		}
		if chain.TmuxSession == "" {
			chain.TmuxSession = steps[i].TmuxSession
		}
		if steps[i].PaneID == "" {
			steps[i].PaneID = chain.PaneID
		}
		if chain.PaneID == "" {
			chain.PaneID = steps[i].PaneID
		}
		if steps[i].AgentType == "" {
			steps[i].AgentType = chain.RootAgentType
		}
		if steps[i].EventName == "" {
			steps[i].EventName = chain.RootEventName
		}
		if steps[i].CreatedAt == 0 {
			steps[i].CreatedAt = time.Now().UnixNano() + int64(i)
		}
	}

	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Seq != steps[j].Seq {
			return steps[i].Seq < steps[j].Seq
		}
		if steps[i].CreatedAt != steps[j].CreatedAt {
			return steps[i].CreatedAt < steps[j].CreatedAt
		}
		return steps[i].StepID < steps[j].StepID
	})

	seen := make(map[string]struct{}, len(steps))
	for i := range steps {
		if steps[i].ParentStepID != "" {
			if _, ok := seen[steps[i].ParentStepID]; !ok {
				return TraceChain{}, nil, fmt.Errorf("step %s references missing parent step %s", steps[i].StepID, steps[i].ParentStepID)
			}
		}
		seen[steps[i].StepID] = struct{}{}
	}

	if len(steps) > 0 {
		first := steps[0]
		last := steps[len(steps)-1]
		if chain.StartedAt == 0 {
			chain.StartedAt = first.CreatedAt
		}
		if chain.CompletedAt == 0 {
			chain.CompletedAt = last.CreatedAt
		}
		if chain.TmuxSession == "" {
			chain.TmuxSession = first.TmuxSession
		}
		if chain.PaneID == "" {
			chain.PaneID = first.PaneID
		}
		if chain.RootAgentType == "" {
			chain.RootAgentType = first.AgentType
		}
		if chain.RootEventName == "" {
			chain.RootEventName = first.EventName
		}
		if chain.RootReason == "" {
			chain.RootReason = first.Reason
		}
		chain.LatestStepKind = last.Kind
		chain.LatestDecision = last.Decision
		chain.LatestStepReason = last.Reason
	} else {
		now := time.Now().UnixNano()
		if chain.StartedAt == 0 {
			chain.StartedAt = now
		}
		if chain.CompletedAt == 0 {
			chain.CompletedAt = chain.StartedAt
		}
	}

	if chain.CompletedAt == 0 {
		chain.CompletedAt = chain.StartedAt
	}
	chain.StepCount = len(steps)
	return chain, steps, nil
}

func pruneTraceChains(tx *sql.Tx, maxChains, maxSteps int) error {
	if maxChains <= 0 || maxSteps <= 0 {
		return nil
	}

	rows, err := tx.Query(`
		SELECT chain_id, step_count
		FROM agent_trace_chains
		ORDER BY started_at ASC, chain_id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type chainStat struct {
		chainID   string
		stepCount int
	}

	stats := make([]chainStat, 0, 64)
	totalSteps := 0
	for rows.Next() {
		var stat chainStat
		if err := rows.Scan(&stat.chainID, &stat.stepCount); err != nil {
			return err
		}
		stats = append(stats, stat)
		totalSteps += stat.stepCount
	}
	if err := rows.Err(); err != nil {
		return err
	}

	totalChains := len(stats)
	var evict []string
	for _, stat := range stats {
		if totalChains <= maxChains && totalSteps <= maxSteps {
			break
		}
		evict = append(evict, stat.chainID)
		totalChains--
		totalSteps -= stat.stepCount
	}
	if len(evict) == 0 {
		return nil
	}

	placeholders := strings.Repeat("?,", len(evict))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(evict))
	for i, chainID := range evict {
		args[i] = chainID
	}
	_, err = tx.Exec(fmt.Sprintf(`DELETE FROM agent_trace_chains WHERE chain_id IN (%s)`, placeholders), args...)
	return err
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}
