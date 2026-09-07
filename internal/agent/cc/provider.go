package cc

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// Provider implements agent.AgentProvider for Claude Code.
type Provider struct {
	prober   *probe.Prober
	tmuxExec tmux.Executor
	cfg      *config.Config
	cfgMu    *sync.RWMutex
}

// NewProvider creates a CC provider. Pass nil for prober/tmuxExec during testing.
func NewProvider(prober *probe.Prober, tmuxExec tmux.Executor, cfg *config.Config, cfgMu *sync.RWMutex) *Provider {
	return &Provider{prober: prober, tmuxExec: tmuxExec, cfg: cfg, cfgMu: cfgMu}
}

func (p *Provider) Type() string        { return "cc" }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) IconHint() string    { return "cc" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "cc"
	}
	if p.prober == nil {
		return false
	}
	if ctx.TmuxTarget == "" {
		return false
	}
	return p.prober.IsAliveFor("cc", ctx.TmuxTarget)
}

func (p *Provider) Identify(proc agent.ProcessInfo) bool {
	exeName := strings.ToLower(filepath.Base(proc.ExePath))
	if p.isConfiguredCommand(exeName) {
		return true
	}
	if isJSRuntimeProcess(exeName, proc.Argv) {
		return true
	}
	return false
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCCStatus(eventName, rawEvent)
}

// IdentifyEvent implements agent.SessionIdentifier. cc puts session_id and cwd
// at the top level of every hook payload (spec §3.1), so the shared extractor
// covers every event and purdexName is not consulted.
func (p *Provider) IdentifyEvent(purdexName string, rawEvent json.RawMessage) (string, string) {
	return agent.ExtractSessionIdentity(rawEvent)
}

// SupportedStatuses declares the Status values cc.Provider's DeriveStatus may
// emit, derived from Events().EmitsStatus. Events() is the single source of
// truth; this shim exists so the StatusSupporter contract (Phase 1) keeps
// working. Return order is lexicographic for determinism (see
// DeriveSupportedStatuses). drift_test.go enforces declaration ↔ emit parity.
func (p *Provider) SupportedStatuses() []agent.Status {
	return agent.DeriveSupportedStatuses(p.Events())
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	if p.prober == nil {
		return false
	}
	return p.prober.IsAliveFor("cc", tmuxTarget)
}

// RegisterServices registers this provider's services into the core service registry.
func (p *Provider) RegisterServices(registry *core.ServiceRegistry) {
	registry.Register(HistoryKey, CCHistoryProvider(p))
	registry.Register(OperatorKey, CCOperator(p))
}

func (p *Provider) CheckStatusline() (agent.StatuslineState, error) {
	path, err := ccSettingsPath()
	if err != nil {
		return agent.StatuslineState{}, err
	}
	return detectStatuslineMode(path)
}

func (p *Provider) InstallStatuslinePdx(pdxPath string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslinePdx(path, pdxPath)
}

func (p *Provider) InstallStatuslineWrap(pdxPath, inner string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslineWrap(path, pdxPath, inner)
}

func (p *Provider) RemoveStatusline() error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return removeStatusline(path)
}

func (p *Provider) isConfiguredCommand(exeName string) bool {
	if exeName == "" {
		return false
	}
	if exeName == "claude" {
		return true
	}
	if p.cfg == nil || p.cfgMu == nil {
		return false
	}
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	for _, cmd := range p.cfg.Detect.CCCommands {
		if strings.EqualFold(filepath.Base(cmd), exeName) {
			return true
		}
	}
	return false
}

func isJSRuntimeProcess(exeName string, argv []string) bool {
	if !agent.IsJSRuntime(exeName) {
		return false
	}
	return argvContainsCCWrapper(argv)
}

func argvContainsCCWrapper(argv []string) bool {
	return agent.ArgvContainsFragment(argv, "@anthropic-ai/claude-code", "/claude-code/", "claude-code/index.js")
}
