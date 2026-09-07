package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const (
	defaultTraceMaxChains = 10000
	defaultTraceMaxSteps  = 100000
)

// TraceChain is the summary row for a trace chain.
type TraceChain struct {
	ChainID          string `json:"chain_id"`
	StartedAt        int64  `json:"started_at"`
	CompletedAt      int64  `json:"completed_at"`
	TerminalStatus   string `json:"terminal_status"`
	TerminalReason   string `json:"terminal_reason"`
	TmuxSession      string `json:"tmux_session"`
	PaneID           string `json:"pane_id"`
	RootAgentType    string `json:"root_agent_type"`
	RootEventName    string `json:"root_event_name"`
	RootReason       string `json:"root_reason"`
	LatestStepKind   string `json:"latest_step_kind"`
	LatestDecision   string `json:"latest_decision"`
	LatestStepReason string `json:"latest_step_reason"`
	StepCount        int    `json:"step_count,omitempty"`
}

// TraceStep is a single ordered trace step within a chain.
type TraceStep struct {
	StepID        string          `json:"step_id"`
	ChainID       string          `json:"chain_id"`
	ParentStepID  string          `json:"parent_step_id,omitempty"`
	Seq           int             `json:"seq"`
	Kind          string          `json:"kind"`
	TmuxSession   string          `json:"tmux_session"`
	PaneID        string          `json:"pane_id"`
	AgentType     string          `json:"agent_type"`
	FrameID       string          `json:"frame_id"`
	ParentFrameID string          `json:"parent_frame_id,omitempty"`
	EventName     string          `json:"event_name"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason"`
	PayloadJSON   json.RawMessage `json:"payload_json,omitempty"`
	BeforeJSON    json.RawMessage `json:"before_json,omitempty"`
	AfterJSON     json.RawMessage `json:"after_json,omitempty"`
	CreatedAt     int64           `json:"created_at"`
}

// TraceRecord combines a chain summary with its ordered steps.
type TraceRecord struct {
	Chain TraceChain  `json:"chain"`
	Steps []TraceStep `json:"steps"`
}

// TraceListFilter filters and paginates trace chains.
type TraceListFilter struct {
	TmuxSession string
	PaneID      string
	AgentType   string
	EventName   string
	Limit       int
	Cursor      string
	Before      bool
}

// TraceChainPage is a page of chain summaries.
type TraceChainPage struct {
	Chains     []TraceChain
	NextCursor string
}

// TraceStore persists trace chains and ordered steps.
type TraceStore struct {
	db        *sql.DB
	maxChains int
	maxSteps  int
}

// sqlQuerier is satisfied by both *sql.DB and *sql.Conn so migration helpers
// can be called on a pinned connection without duplicating function bodies.
type sqlQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
