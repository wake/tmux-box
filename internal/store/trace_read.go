package store

// The trace store's read path: chain listing with cursor pagination, and full
// chain records with their steps.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ListChains returns chain summaries ordered newest-first.
func (s *TraceStore) ListChains(filter TraceListFilter) (TraceChainPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	limit++

	query, args, err := buildTraceChainListQuery(filter, limit)
	if err != nil {
		return TraceChainPage{}, err
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TraceChainPage{}, err
	}
	defer rows.Close()

	chains, err := collectTraceChains(rows)
	if err != nil {
		return TraceChainPage{}, err
	}

	page := TraceChainPage{Chains: chains}
	if len(page.Chains) > limit-1 {
		last := page.Chains[limit-2]
		page.NextCursor = encodeTraceCursor(last.StartedAt, last.ChainID)
		page.Chains = page.Chains[:limit-1]
	}
	return page, nil
}

// GetChainRecord returns a full chain and its ordered steps.
func (s *TraceStore) GetChainRecord(chainID string) (*TraceRecord, error) {
	return getTraceChainRecord(context.Background(), s.db, chainID)
}

// getTraceChainRecord reads a chain and its steps through the given querier.
func getTraceChainRecord(ctx context.Context, q sqlQuerier, chainID string) (*TraceRecord, error) {
	var chain TraceChain
	err := q.QueryRowContext(ctx, `
		SELECT chain_id, started_at, completed_at, terminal_status, terminal_reason,
		       tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
		       latest_step_kind, latest_decision, latest_step_reason, step_count
		FROM agent_trace_chains
		WHERE chain_id = ?
	`, chainID).Scan(
		&chain.ChainID,
		&chain.StartedAt,
		&chain.CompletedAt,
		&chain.TerminalStatus,
		&chain.TerminalReason,
		&chain.TmuxSession,
		&chain.PaneID,
		&chain.RootAgentType,
		&chain.RootEventName,
		&chain.RootReason,
		&chain.LatestStepKind,
		&chain.LatestDecision,
		&chain.LatestStepReason,
		&chain.StepCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// The payload of a deduped step lives on the chain row, so flag and payload
	// must come from one snapshot. Resolving it in the JOIN makes that
	// structural: reading the chain and the steps as two independent queries
	// and pairing them in Go would let an interleaved SaveChain hand root B's
	// payload to root A's steps.
	//
	// collectTraceSteps scans positionally, so the column order below is load
	// bearing — the CASE has to stay in the payload column's position.
	rows, err := q.QueryContext(ctx, `
		SELECT s.step_id, s.chain_id, s.parent_step_id, s.seq, s.kind, s.tmux_session, s.pane_id,
		       s.agent_type, s.frame_id, s.parent_frame_id, s.event_name, s.decision, s.reason,
		       CASE WHEN s.payload_is_root = 1
		            THEN c.root_payload_json ELSE s.payload_json END AS payload_json,
		       s.before_json, s.after_json, s.created_at
		FROM agent_trace_steps s
		JOIN agent_trace_chains c ON c.chain_id = s.chain_id
		WHERE s.chain_id = ?
		ORDER BY s.seq ASC, s.created_at ASC, s.step_id ASC
	`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps, err := collectTraceSteps(rows)
	if err != nil {
		return nil, err
	}
	chain.StepCount = len(steps)
	return &TraceRecord{Chain: chain, Steps: steps}, nil
}

func buildTraceChainListQuery(filter TraceListFilter, limit int) (string, []any, error) {
	var clauses []string
	var args []any

	if filter.TmuxSession != "" {
		clauses = append(clauses, "c.tmux_session = ?")
		args = append(args, filter.TmuxSession)
	}
	if filter.PaneID != "" {
		clauses = append(clauses, "c.pane_id = ?")
		args = append(args, filter.PaneID)
	}
	if filter.AgentType != "" {
		clauses = append(clauses, "c.root_agent_type = ?")
		args = append(args, filter.AgentType)
	}
	if filter.EventName != "" {
		clauses = append(clauses, "c.root_event_name = ?")
		args = append(args, filter.EventName)
	}
	if filter.Cursor != "" {
		startedAt, chainID, err := decodeTraceCursor(filter.Cursor)
		if err != nil {
			return "", nil, err
		}
		op := "<"
		if !filter.Before {
			op = ">"
		}
		clauses = append(clauses, fmt.Sprintf("(c.started_at %s ? OR (c.started_at = ? AND c.chain_id %s ?))", op, op))
		args = append(args, startedAt, startedAt, chainID)
	}

	query := `
		SELECT c.chain_id, c.started_at, c.completed_at, c.terminal_status, c.terminal_reason,
		       c.tmux_session, c.pane_id, c.root_agent_type, c.root_event_name, c.root_reason,
		       c.latest_step_kind, c.latest_decision, c.latest_step_reason, c.step_count
		FROM agent_trace_chains c
	`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += `
		ORDER BY c.started_at DESC, c.chain_id DESC
		LIMIT ?
	`
	args = append(args, limit)
	return query, args, nil
}

func collectTraceChains(rows *sql.Rows) ([]TraceChain, error) {
	var chains []TraceChain
	for rows.Next() {
		var chain TraceChain
		if err := rows.Scan(
			&chain.ChainID,
			&chain.StartedAt,
			&chain.CompletedAt,
			&chain.TerminalStatus,
			&chain.TerminalReason,
			&chain.TmuxSession,
			&chain.PaneID,
			&chain.RootAgentType,
			&chain.RootEventName,
			&chain.RootReason,
			&chain.LatestStepKind,
			&chain.LatestDecision,
			&chain.LatestStepReason,
			&chain.StepCount,
		); err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}

func collectTraceSteps(rows *sql.Rows) ([]TraceStep, error) {
	var steps []TraceStep
	for rows.Next() {
		var step TraceStep
		var parent sql.NullString
		var payload, before, after string
		if err := rows.Scan(
			&step.StepID,
			&step.ChainID,
			&parent,
			&step.Seq,
			&step.Kind,
			&step.TmuxSession,
			&step.PaneID,
			&step.AgentType,
			&step.FrameID,
			&step.ParentFrameID,
			&step.EventName,
			&step.Decision,
			&step.Reason,
			&payload,
			&before,
			&after,
			&step.CreatedAt,
		); err != nil {
			return nil, err
		}
		step.ParentStepID = parent.String
		step.PayloadJSON = json.RawMessage(payload)
		step.BeforeJSON = json.RawMessage(before)
		step.AfterJSON = json.RawMessage(after)
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func encodeTraceCursor(startedAt int64, chainID string) string {
	return strconv.FormatInt(startedAt, 10) + "|" + chainID
}

func decodeTraceCursor(cursor string) (int64, string, error) {
	startedAtText, chainID, ok := strings.Cut(cursor, "|")
	if !ok {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	startedAt, err := strconv.ParseInt(startedAtText, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid trace cursor %q: %w", cursor, err)
	}
	if chainID == "" {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	return startedAt, chainID, nil
}
